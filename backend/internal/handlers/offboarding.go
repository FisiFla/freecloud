package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// OffboardResponse is the JSON response for the offboard endpoint.
type OffboardResponse struct {
	UserID                  string   `json:"userId"`
	SessionsTerminated      bool     `json:"sessionsTerminated"`
	SessionTerminationError string   `json:"sessionTerminationError,omitempty"`
	DevicesWiped            int      `json:"devicesWiped"`
	DevicesFailed           int      `json:"devicesFailed"`
	Warnings                []string `json:"warnings,omitempty"`
}

// Offboard handles user offboarding (panic button).
func (h *Handler) Offboard(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		respondError(w, http.StatusBadRequest, "userId is required")
		return
	}
	if !isValidUUID(userID) {
		respondError(w, http.StatusBadRequest, "userId must be a valid UUID")
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()
	logger := h.logger

	logger.Info("offboarding user",
		zap.String("user_id", userID),
		zap.String("actor_id", actorID),
	)

	var devicesWiped int
	var devicesFailed int
	var sessionsTerminated bool
	var sessionError string
	var warnings []string

	// Sequential best-effort offboarding — each step continues regardless of failures

	// Step 1: Disable user in Keycloak
	if err := h.keycloak.DisableUser(ctx, userID); err != nil {
		logger.Warn("failed to disable user in Keycloak", zap.String("user_id", userID), zap.Error(err))
		warnings = append(warnings, "failed to disable user in identity provider")
	}
	// Always best-effort soft-disable in local DB regardless of Keycloak outcome,
	// so local state reflects the intent even if Keycloak is unreachable.
	if h.db != nil {
		_, dbErr := h.db.Exec(ctx,
			`UPDATE users SET disabled = true, updated_at = NOW() WHERE keycloak_user_id = $1`,
			userID,
		)
		if dbErr != nil {
			logger.Warn("failed to soft-disable user in local DB",
				zap.String("user_id", userID),
				zap.Error(dbErr),
			)
			warnings = append(warnings, "failed to mark user disabled locally")
		}
	}

	// Step 2: Logout all sessions
	if err := h.keycloak.LogoutAllSessions(ctx, userID); err != nil {
		logger.Warn("failed to logout sessions", zap.String("user_id", userID), zap.Error(err))
		sessionError = "session termination failed"
		warnings = append(warnings, "failed to terminate user sessions")
	} else {
		sessionsTerminated = true
	}

	// Step 3: Look up device IDs
	var deviceIDs []string
	if h.db != nil {
		rows, err := h.db.Query(ctx,
			`SELECT device_id FROM users_devices_mapping WHERE user_id = $1`,
			userID,
		)
		if err != nil {
			logger.Error("failed to query device mapping", zap.Error(err))
			warnings = append(warnings, "failed to look up user devices")
		} else {
			for rows.Next() {
				var devID string
				if err := rows.Scan(&devID); err != nil {
					logger.Warn("failed to scan device ID", zap.Error(err))
					continue
				}
				deviceIDs = append(deviceIDs, devID)
			}
			if err := rows.Err(); err != nil {
				logger.Error("error iterating device rows", zap.Error(err))
				warnings = append(warnings, "failed to read user devices")
			}
			rows.Close()
		}
	}

	// Step 4: Wipe each device best-effort (sequential, no shared mutation)
	for _, devID := range deviceIDs {
		if err := h.fleet.IssueRemoteWipe(ctx, devID); err != nil {
			logger.Error("failed to wipe device",
				zap.String("device_id", devID),
				zap.Error(err),
			)
			devicesFailed++
		} else {
			devicesWiped++
		}
	}

	// Write immutable audit log
	details, _ := json.Marshal(map[string]interface{}{
		"devices_wiped":  devicesWiped,
		"devices_failed": devicesFailed,
		"device_ids":     deviceIDs,
	})
	if h.db != nil {
		_, auditErr := h.db.Exec(ctx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, $3, $4, $5)`,
			actorID, "offboard", "user", userID, details,
		)
		if auditErr != nil {
			logger.Warn("failed to write audit log", zap.Error(auditErr))
		}
	}

	respondJSON(w, http.StatusOK, OffboardResponse{
		UserID:                  userID,
		SessionsTerminated:      sessionsTerminated,
		SessionTerminationError: sessionError,
		DevicesWiped:            int(devicesWiped),
		DevicesFailed:           int(devicesFailed),
		Warnings:                warnings,
	})
}
