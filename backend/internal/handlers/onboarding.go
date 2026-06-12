package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Nerzal/gocloak/v13"
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
	Warning         string        `json:"warning,omitempty"`
}

// Onboard handles user onboarding.
func (h *Handler) Onboard(w http.ResponseWriter, r *http.Request) {
	logger := h.logger

	var req OnboardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.FirstName == "" || req.LastName == "" || req.Email == "" {
		http.Error(w, `{"error":"firstName, lastName, and email are required"}`, http.StatusBadRequest)
		return
	}

	actorID := middleware.GetActorID(r.Context())

	// Use errgroup to run Keycloak and Fleet operations concurrently
	g, ctx := errgroup.WithContext(r.Context())

	var createdUser *gocloak.User
	var enrollmentToken string
	var fleetErr error

	// Goroutine 1: Create user in Keycloak
	g.Go(func() error {
		user, err := h.keycloak.CreateUser(ctx, req.FirstName, req.LastName, req.Email, req.Department)
		if err != nil {
			return err
		}
		createdUser = user
		// Insert into local database
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
			http.Error(w, `{"error":"keycloak user creation failed: `+err.Error()+`"}`, http.StatusInternalServerError)
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
	_, auditErr := h.db.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		actorID, "onboard", "user", targetID, string(details),
	)
	if auditErr != nil {
		logger.Warn("failed to write audit log", zap.Error(auditErr))
	}

	resp := OnboardResponse{
		User:            createdUser,
		EnrollmentToken: enrollmentToken,
		EnrollmentURL:   enrollmentURL,
	}

	if fleetErr != nil {
		resp.Warning = "FleetDM enrollment token creation failed: " + fleetErr.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
