package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/notify"
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

	// Step 1: Disable user in Keycloak. This is the critical lock; if it fails
	// the account is NOT reliably disabled, so the whole offboard reports a
	// non-2xx status (below) instead of silently returning 200.
	disableFailed := false
	if err := h.keycloak.DisableUser(ctx, userID); err != nil {
		logger.Warn("failed to disable user in Keycloak", zap.String("user_id", userID), zap.Error(err))
		warnings = append(warnings, "failed to disable user in identity provider")
		disableFailed = true
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

	if h.db != nil {
		if auditErr := h.writeAuditEntryBestEffort(actorID, "offboard", "user", userID, map[string]interface{}{
			"devices_wiped":  devicesWiped,
			"devices_failed": devicesFailed,
			"device_ids":     deviceIDs,
		}); auditErr != nil {
			logger.Warn("failed to write audit log", zap.Error(auditErr))
			warnings = append(warnings, "failed to write offboard audit log")
		}
	}

	// Fire event notification (fail-open: runs in background, never blocks response).
	if h.notifier != nil {
		n := h.notifier
		go func() {
			notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = n.Notify(notifyCtx, notify.Event{
				Type:     notify.EventOffboardCompleted,
				ActorID:  actorID,
				TargetID: userID,
				Details: map[string]any{
					"devices_wiped":  devicesWiped,
					"devices_failed": devicesFailed,
				},
			})
		}()
	}

	// If the account couldn't be disabled, return a non-2xx so callers and
	// monitoring escalate rather than treating a half-done panic-button as OK.
	status := http.StatusOK
	if disableFailed {
		status = http.StatusBadGateway
	}
	respondJSON(w, status, OffboardResponse{
		UserID:                  userID,
		SessionsTerminated:      sessionsTerminated,
		SessionTerminationError: sessionError,
		DevicesWiped:            int(devicesWiped),
		DevicesFailed:           int(devicesFailed),
		Warnings:                warnings,
	})
}
