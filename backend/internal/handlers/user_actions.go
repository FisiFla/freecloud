package handlers

// A4 — PATCH /api/v1/users/{id} : update user profile
// A5 — POST  /api/v1/users/{id}/reset-password : admin-triggered password reset

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// PatchUserRequest is the JSON request body for PATCH /api/v1/users/{id}.
// All fields are optional; only non-empty fields are applied.
type PatchUserRequest struct {
	FirstName  *string `json:"firstName,omitempty"`
	LastName   *string `json:"lastName,omitempty"`
	Department *string `json:"department,omitempty"`
	Role       *string `json:"role,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

// PatchUser updates mutable user profile fields in Keycloak + local DB.
func (h *Handler) PatchUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" || !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "valid user id is required")
		return
	}

	var req PatchUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// At least one field must be provided.
	if req.FirstName == nil && req.LastName == nil && req.Department == nil && req.Role == nil && req.Enabled == nil {
		respondError(w, http.StatusBadRequest, "at least one field must be provided")
		return
	}

	// Validate lengths before hitting the DB.
	var valErrors []ValidationError
	if req.FirstName != nil {
		*req.FirstName = strings.TrimSpace(*req.FirstName)
		if *req.FirstName == "" {
			valErrors = append(valErrors, ValidationError{Field: "firstName", Message: "firstName must not be empty"})
		} else if len(*req.FirstName) > 100 {
			valErrors = append(valErrors, ValidationError{Field: "firstName", Message: "firstName must be ≤ 100 characters"})
		}
	}
	if req.LastName != nil {
		*req.LastName = strings.TrimSpace(*req.LastName)
		if *req.LastName == "" {
			valErrors = append(valErrors, ValidationError{Field: "lastName", Message: "lastName must not be empty"})
		} else if len(*req.LastName) > 100 {
			valErrors = append(valErrors, ValidationError{Field: "lastName", Message: "lastName must be ≤ 100 characters"})
		}
	}
	if req.Department != nil {
		*req.Department = strings.TrimSpace(*req.Department)
		if len(*req.Department) > 100 {
			valErrors = append(valErrors, ValidationError{Field: "department", Message: "department must be ≤ 100 characters"})
		}
	}
	if req.Role != nil {
		*req.Role = strings.TrimSpace(*req.Role)
		if len(*req.Role) > 100 {
			valErrors = append(valErrors, ValidationError{Field: "role", Message: "role must be ≤ 100 characters"})
		}
	}
	if len(valErrors) > 0 {
		respondValidationErrors(w, valErrors)
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()

	// Load current values from DB to merge
	var (
		curFirstName, curLastName, curDepartment, curRole string
		curDisabled                                        bool
		curCreatedAt, curUpdatedAt                         time.Time
	)
	err := h.db.QueryRow(ctx,
		`SELECT first_name, last_name, COALESCE(department,''), COALESCE(role,''),
		        COALESCE(disabled,false), created_at, updated_at
		 FROM users WHERE keycloak_user_id = $1`, userID,
	).Scan(&curFirstName, &curLastName, &curDepartment, &curRole, &curDisabled, &curCreatedAt, &curUpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("failed to load user for patch", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Merge patch fields onto current values
	newFirst := curFirstName
	newLast := curLastName
	newDept := curDepartment
	newRole := curRole
	newEnabled := !curDisabled

	if req.FirstName != nil {
		newFirst = *req.FirstName
	}
	if req.LastName != nil {
		newLast = *req.LastName
	}
	if req.Department != nil {
		newDept = *req.Department
	}
	if req.Role != nil {
		newRole = *req.Role
	}
	if req.Enabled != nil {
		newEnabled = *req.Enabled
	}

	// Propagate name/enabled to Keycloak
	if err := h.keycloak.UpdateUser(ctx, userID, newFirst, newLast, newDept, newEnabled); err != nil {
		h.logger.Error("failed to update keycloak user", zap.String("user_id", userID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to update user in identity provider")
		return
	}

	// Persist to local DB
	_, dbErr := h.db.Exec(ctx,
		`UPDATE users SET first_name=$1, last_name=$2, department=$3, role=$4, disabled=$5, updated_at=NOW()
		 WHERE keycloak_user_id=$6`,
		newFirst, newLast, newDept, newRole, !newEnabled, userID)
	if dbErr != nil {
		h.logger.Error("failed to update user in db", zap.String("user_id", userID), zap.Error(dbErr))
		respondError(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	// Audit log (detached context)
	actorID := middleware.GetActorID(ctx)
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = h.db.Exec(auditCtx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		actorID, "user_update", "user", userID,
		map[string]interface{}{
			"first_name": newFirst, "last_name": newLast,
			"department": newDept, "role": newRole, "enabled": newEnabled,
		})

	respondJSON(w, http.StatusOK, User{
		ID:             userID,
		KeycloakUserID: userID,
		FirstName:      newFirst,
		LastName:       newLast,
		Department:     newDept,
		Role:           newRole,
		Disabled:       !newEnabled,
		CreatedAt:      curCreatedAt.Format(time.RFC3339),
		UpdatedAt:      time.Now().Format(time.RFC3339),
		Devices:        []Device{},
	})
}

// ResetPassword triggers a Keycloak password reset action email for a user.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" || !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "valid user id is required")
		return
	}

	ctx := r.Context()

	// Verify user exists locally
	if h.db != nil {
		var email string
		if err := h.db.QueryRow(ctx,
			`SELECT email FROM users WHERE keycloak_user_id = $1`, userID,
		).Scan(&email); err != nil {
			if err == pgx.ErrNoRows {
				respondError(w, http.StatusNotFound, "user not found")
				return
			}
			h.logger.Error("failed to look up user for password reset", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if err := h.keycloak.SendPasswordReset(ctx, userID); err != nil {
		h.logger.Error("failed to send password reset", zap.String("user_id", userID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to send password reset email")
		return
	}

	// Audit log (detached context — this is a privileged security action)
	actorID := middleware.GetActorID(ctx)
	if h.db != nil {
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.db.Exec(auditCtx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "user_password_reset", "user", userID,
			map[string]interface{}{"triggered_by": actorID})
	}

	respondJSON(w, http.StatusOK, map[string]bool{"sent": true})
}
