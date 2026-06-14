package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Nerzal/gocloak/v13"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// OnboardRequest is the JSON request body for the onboard endpoint.
type OnboardRequest struct {
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	Email      string `json:"email"`
	Department string `json:"department"`
	Role       string `json:"role"`
}

// OnboardResponse is the JSON response for the onboard endpoint.
type OnboardResponse struct {
	User            *gocloak.User `json:"user"`
	EnrollmentToken string        `json:"enrollmentToken"`
	EnrollmentURL   string        `json:"enrollmentURL"`
	NextStep        string        `json:"nextStep,omitempty"`
	Warning         string        `json:"warning,omitempty"`
}

// Onboard handles user onboarding.
func (h *Handler) Onboard(w http.ResponseWriter, r *http.Request) {
	logger := h.logger

	var req OnboardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Normalize input before validation
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Department = strings.TrimSpace(req.Department)
	req.Role = strings.TrimSpace(req.Role)

	// Validate required fields
	var valErrors []ValidationError
	if req.FirstName == "" {
		valErrors = append(valErrors, ValidationError{Field: "firstName", Message: "firstName is required"})
	} else if len(req.FirstName) > 100 {
		valErrors = append(valErrors, ValidationError{Field: "firstName", Message: "firstName must be ≤ 100 characters"})
	}
	if req.LastName == "" {
		valErrors = append(valErrors, ValidationError{Field: "lastName", Message: "lastName is required"})
	} else if len(req.LastName) > 100 {
		valErrors = append(valErrors, ValidationError{Field: "lastName", Message: "lastName must be ≤ 100 characters"})
	}
	if req.Email == "" {
		valErrors = append(valErrors, ValidationError{Field: "email", Message: "email is required"})
	} else if len(req.Email) > 254 {
		valErrors = append(valErrors, ValidationError{Field: "email", Message: "email must be ≤ 254 characters"})
	} else if !strings.Contains(req.Email, "@") {
		valErrors = append(valErrors, ValidationError{Field: "email", Message: "email must contain @"})
	}
	if len(req.Department) > 100 {
		valErrors = append(valErrors, ValidationError{Field: "department", Message: "department must be ≤ 100 characters"})
	}
	if len(req.Role) > 100 {
		valErrors = append(valErrors, ValidationError{Field: "role", Message: "role must be ≤ 100 characters"})
	}
	if len(valErrors) > 0 {
		respondValidationErrors(w, valErrors)
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	var warnings []string

	// Idempotency: if this email already maps to a Keycloak user locally, do not
	// create a second Keycloak user — report the existing mapping as a conflict.
	if h.db != nil {
		var existingID string
		lookupErr := h.db.QueryRow(ctx,
			`SELECT keycloak_user_id FROM users WHERE email = $1`, req.Email,
		).Scan(&existingID)
		switch {
		case lookupErr == nil:
			respondError(w, http.StatusConflict, "a user with this email already exists")
			return
		case errors.Is(lookupErr, pgx.ErrNoRows):
			// No existing user — proceed.
		default:
			logger.Error("onboard idempotency lookup failed", zap.Error(lookupErr))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// Step 1: Create the user in Keycloak.
	result, err := h.keycloak.CreateUser(ctx, req.FirstName, req.LastName, req.Email, req.Department)
	if err != nil {
		logger.Error("keycloak user creation failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to create user in identity provider")
		return
	}
	if !result.PasswordSet {
		logger.Error("password could not be set for user")
		respondError(w, http.StatusInternalServerError, "password could not be set for user")
		return
	}
	if result.User == nil || result.User.ID == nil || *result.User.ID == "" {
		logger.Error("keycloak user creation returned missing user ID")
		respondError(w, http.StatusInternalServerError, "keycloak user creation returned missing user ID")
		return
	}
	createdUser := result
	kcUserID := *result.User.ID

	// Compensation: if the Keycloak user was created but local persistence fails,
	// delete the orphaned Keycloak user (mirrors CreateApp in apps.go). A fresh
	// context is used so cleanup still runs if the client disconnected.
	persisted := false
	defer func() {
		if persisted {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Warn("rolling back orphaned Keycloak user", zap.String("kc_user_id", kcUserID))
		if delErr := h.keycloak.DeleteUser(cleanupCtx, kcUserID); delErr != nil {
			logger.Error("failed to roll back orphaned Keycloak user",
				zap.String("kc_user_id", kcUserID), zap.Error(delErr))
		}
	}()

	if result.SetupWarning != "" {
		warnings = append(warnings, result.SetupWarning)
	}

	// Step 2: Create the Fleet enrollment token (best-effort; non-blocking).
	var enrollmentToken string
	token, fleetErr := h.fleet.CreateEnrollmentToken(ctx)
	if fleetErr != nil {
		logger.Warn("fleet enrollment token creation failed, continuing", zap.Error(fleetErr))
		warnings = append(warnings, "Fleet enrollment failed; manual enrollment required")
	} else {
		enrollmentToken = token
	}

	// Step 3: Persist the user row and its audit-log entry atomically. A detached
	// context ensures a client disconnect can't leave the user half-persisted,
	// and binding the audit insert into the same transaction means a successful
	// onboarding can never lack an audit record. Failure here triggers the
	// Keycloak rollback via the deferred compensation above.
	if h.db != nil {
		auditDetails, _ := json.Marshal(map[string]interface{}{
			"email": req.Email, "department": req.Department, "role": req.Role,
		})
		persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if persistErr := h.persistOnboard(persistCtx, kcUserID, req, actorID, string(auditDetails)); persistErr != nil {
			logger.Error("failed to persist onboarded user; rolling back Keycloak user",
				zap.String("kc_user_id", kcUserID), zap.Error(persistErr))
			respondError(w, http.StatusInternalServerError, "failed to persist user")
			return
		}
	}
	persisted = true

	// Devices are linked on a FleetDM enrollment callback (see the enrollment
	// handler), not pre-populated here.

	enrollmentURL := ""
	if enrollmentToken != "" {
		enrollmentURL = "/enroll/" + enrollmentToken
	}

	nextStep := "User created. Admin must provide login credentials to the user."
	if createdUser.ResetEmailSent {
		nextStep = "Password reset email sent to user."
	}

	resp := OnboardResponse{
		User:            createdUser.User,
		EnrollmentToken: enrollmentToken,
		EnrollmentURL:   enrollmentURL,
		NextStep:        nextStep,
	}
	if len(warnings) > 0 {
		resp.Warning = strings.Join(warnings, "; ")
	}

	status := http.StatusOK
	if fleetErr != nil {
		status = http.StatusAccepted
	}
	respondJSON(w, status, resp)
}

// persistOnboard writes the user row and its audit-log entry in a single
// transaction, so a persisted onboarding always has a matching audit record.
func (h *Handler) persistOnboard(ctx context.Context, kcUserID string, req OnboardRequest, actorID, auditDetails string) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO users (keycloak_user_id, email, first_name, last_name, department, role)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		kcUserID, req.Email, req.FirstName, req.LastName, req.Department, req.Role,
	); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		actorID, "onboard", "user", kcUserID, auditDetails,
	); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return tx.Commit(ctx)
}
