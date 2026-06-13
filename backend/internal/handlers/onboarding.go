package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Nerzal/gocloak/v13"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

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

	// Use errgroup to run Keycloak and Fleet operations concurrently
	g, ctx := errgroup.WithContext(r.Context())

	var createdUser *gocloak.User
	var enrollmentToken string
	var fleetErr error
	var dbWarnings []string

	// Goroutine 1: Create user in Keycloak
	g.Go(func() error {
		user, err := h.keycloak.CreateUser(ctx, req.FirstName, req.LastName, req.Email, req.Department)
		if err != nil {
			return err
		}
		createdUser = user
		// Insert into local database
		if h.db != nil {
			_, dbErr := h.db.Exec(ctx,
				`INSERT INTO users (keycloak_user_id, email, first_name, last_name, department, role)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (keycloak_user_id) DO UPDATE
				 SET email = $2, first_name = $3, last_name = $4, department = $5, role = $6, updated_at = NOW()`,
				*user.ID, req.Email, req.FirstName, req.LastName, req.Department, req.Role,
			)
			if dbErr != nil {
				logger.Warn("failed to persist user to local DB, continuing",
					zap.String("user_id", *user.ID),
					zap.Error(dbErr),
				)
				dbWarnings = append(dbWarnings, fmt.Sprintf("user DB insert: %v", dbErr))
			}
		}
		return nil
	})

	// Goroutine 2: Create Fleet enrollment token
	g.Go(func() error {
		token, err := h.fleet.CreateEnrollmentToken(ctx)
		if err != nil {
			fleetErr = err
			return err
		}
		enrollmentToken = token
		return nil
	})

	// Wait for both
	if err := g.Wait(); err != nil {
		logger.Error("onboarding operation failed", zap.Error(err))

		// If Keycloak creation failed, return error
		if createdUser == nil {
			respondError(w, http.StatusInternalServerError, "keycloak user creation failed: "+err.Error())
			return
		}
		// If only Fleet failed, still return success with warning
	}

	// Determine enrollment URL
	enrollmentURL := ""
	if enrollmentToken != "" {
		enrollmentURL = "/enroll/" + enrollmentToken
	}

	// Write audit log
	details, _ := json.Marshal(map[string]interface{}{
		"email":      req.Email,
		"department": req.Department,
		"role":       req.Role,
	})
	var targetID string
	if createdUser != nil && createdUser.ID != nil {
		targetID = *createdUser.ID
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
	if createdUser != nil && createdUser.ID != nil && enrollmentToken != "" && h.db != nil {
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
				*createdUser.ID, deviceID,
			)
			if mapErr != nil {
				logger.Warn("failed to insert device mapping", zap.Error(mapErr))
				dbWarnings = append(dbWarnings, fmt.Sprintf("device mapping insert: %v", mapErr))
			}
		}
	}

	resp := OnboardResponse{
		User:            createdUser,
		EnrollmentToken: enrollmentToken,
		EnrollmentURL:   enrollmentURL,
		NextStep:        "User must log in and set password using temporary credentials.",
	}

	if fleetErr != nil {
		warning := "User created. Fleet enrollment failed — manual enrollment required."
		if len(dbWarnings) > 0 {
			warning += " DB warnings: " + strings.Join(dbWarnings, "; ")
		}
		resp.Warning = warning
		respondJSON(w, http.StatusAccepted, resp)
		return
	}

	if len(dbWarnings) > 0 {
		resp.Warning = strings.Join(dbWarnings, "; ")
		respondJSON(w, http.StatusOK, resp)
		return
	}

	respondJSON(w, http.StatusOK, resp)
}
