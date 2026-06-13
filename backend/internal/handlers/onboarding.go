package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Nerzal/gocloak/v13"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
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
	if len(valErrors) > 0 {
		respondValidationErrors(w, valErrors)
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()
	logger := h.logger

	// Sequential flow: Keycloak → DB persist → Fleet → audit → devices
	var createdUser *keycloak.CreateUserResult
	var enrollmentToken string
	var warnings []string

	// Step 1: Create user in Keycloak
	result, err := h.keycloak.CreateUser(ctx, req.FirstName, req.LastName, req.Email, req.Department)
	if err != nil {
		logger.Error("keycloak user creation failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "keycloak user creation failed: "+err.Error())
		return
	}
	if !result.PasswordSet {
		logger.Error("password could not be set for user")
		respondError(w, http.StatusInternalServerError, "password could not be set for user")
		return
	}
	createdUser = result

	// Capture email setup warning
	if result.SetupWarning != "" {
		warnings = append(warnings, result.SetupWarning)
	}

	// Step 2: Insert into local database
	if h.db != nil {
		_, dbErr := h.db.Exec(ctx,
			`INSERT INTO users (keycloak_user_id, email, first_name, last_name, department, role)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (keycloak_user_id) DO UPDATE
			 SET email = $2, first_name = $3, last_name = $4, department = $5, role = $6, updated_at = NOW()`,
			*result.User.ID, req.Email, req.FirstName, req.LastName, req.Department, req.Role,
		)
		if dbErr != nil {
			logger.Warn("failed to persist user to local DB, continuing",
				zap.String("user_id", *result.User.ID),
				zap.Error(dbErr),
			)
			warnings = append(warnings, fmt.Sprintf("user DB insert: %v", dbErr))
		}
	}

	// Step 3: Create Fleet enrollment token
	token, fleetErr := h.fleet.CreateEnrollmentToken(ctx)
	if fleetErr != nil {
		logger.Warn("fleet enrollment token creation failed, continuing", zap.Error(fleetErr))
		warnings = append(warnings, "Fleet enrollment failed; manual enrollment required")
	} else {
		enrollmentToken = token
	}

	// Step 4: Determine enrollment URL
	enrollmentURL := ""
	if enrollmentToken != "" {
		enrollmentURL = "/enroll/" + enrollmentToken
	}

	// Step 5: Write audit log
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"email":      req.Email,
		"department": req.Department,
		"role":       req.Role,
	})
	var targetID string
	if createdUser != nil && createdUser.User != nil && createdUser.User.ID != nil {
		targetID = *createdUser.User.ID
	} else {
		targetID = req.Email
	}
	if h.db != nil {
		_, auditErr := h.db.Exec(ctx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "onboard", "user", targetID, string(details),
		)
		if auditErr != nil {
			logger.Warn("failed to write audit log", zap.Error(auditErr))
			dbWarnings = append(dbWarnings, fmt.Sprintf("audit log insert: %v", auditErr))
		}
	}

	// Wire devices: create placeholder device and mapping when we have a user and enrollment token
	if createdUser != nil && createdUser.User != nil && createdUser.User.ID != nil && enrollmentToken != "" && h.db != nil {
		deviceID := uuid.New().String()
		hostname := "pending-" + enrollmentToken[:8]
		_, devErr := h.db.Exec(ctx,
			`INSERT INTO devices (fleet_host_id, hostname, os_version)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (fleet_host_id) DO NOTHING`,
			deviceID, hostname, "pending",
		)
		if devErr != nil {
			logger.Warn("failed to insert placeholder device", zap.Error(devErr))
			dbWarnings = append(dbWarnings, fmt.Sprintf("device insert: %v", devErr))
		} else {
			_, mapErr := h.db.Exec(ctx,
				`INSERT INTO users_devices_mapping (user_id, device_id)
				 VALUES ($1, $2)
				 ON CONFLICT (user_id, device_id) DO NOTHING`,
				*createdUser.User.ID, deviceID,
			)
			if mapErr != nil {
				logger.Warn("failed to insert device mapping", zap.Error(mapErr))
				dbWarnings = append(dbWarnings, fmt.Sprintf("device mapping insert: %v", mapErr))
			}
		}
	}

	nextStep := "User created. Admin must provide login credentials to the user."
	if createdUser != nil && createdUser.ResetEmailSent {
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
