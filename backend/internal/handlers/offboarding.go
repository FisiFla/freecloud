package handlers

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// OffboardResponse is the JSON response for the offboard endpoint.
type OffboardResponse struct {
	UserID                  string `json:"userId"`
	SessionsTerminated       bool   `json:"sessionsTerminated"`
	SessionTerminationError  string `json:"sessionTerminationError,omitempty"`
	DevicesWiped             int    `json:"devicesWiped"`
	DevicesFailed            int    `json:"devicesFailed"`
}

// Offboard handles user offboarding (panic button).
func (h *Handler) Offboard(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		respondError(w, http.StatusBadRequest, "userId is required")
		return
	}

	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()
	logger := h.logger

	logger.Info("offboarding user",
		zap.String("user_id", userID),
		zap.String("actor_id", actorID),
	)

	var devicesWiped int64
	var devicesFailed int64
	var sessionsTerminated bool
	var sessionError string

	// Use errgroup for concurrent operations
	g, ctx := errgroup.WithContext(ctx)

	// Task 1: Disable user in Keycloak
	g.Go(func() error {
		if err := h.keycloak.DisableUser(ctx, userID); err != nil {
			return err
		}
		// Soft-disable user in local DB
		if h.db != nil {
			_, dbErr := h.db.Exec(ctx,
				`UPDATE users SET role = CONCAT(COALESCE(role, ''), ' (DISABLED)'), updated_at = NOW() WHERE keycloak_user_id = $1`,
				userID,
			)
			if dbErr != nil {
				logger.Warn("failed to soft-disable user in local DB",
					zap.String("user_id", userID),
					zap.Error(dbErr),
				)
			}
		}
		return nil
	})

	// Task 2: Logout all sessions
	g.Go(func() error {
		if err := h.keycloak.LogoutAllSessions(ctx, userID); err != nil {
			logger.Warn("failed to logout sessions",
				zap.String("user_id", userID),
				zap.Error(err),
			)
			sessionError = err.Error()
			return nil // don't fail the whole offboard
		}
		sessionsTerminated = true
		return nil
	})

	// Task 3: Query device mappings and wipe each device
	var deviceIDs []string
	g.Go(func() error {
		if h.db == nil {
			return nil
		}
		rows, err := h.db.Query(ctx,
			`SELECT device_id FROM users_devices_mapping WHERE user_id = $1`,
			userID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var devID string
			if err := rows.Scan(&devID); err != nil {
				logger.Warn("failed to scan device ID", zap.Error(err))
				continue
			}
			deviceIDs = append(deviceIDs, devID)
		}
		return rows.Err()
	})

	// Wait for the queries to complete
	if err := g.Wait(); err != nil {
		logger.Error("offboarding query failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "offboarding failed: "+err.Error())
		return
	}

	// Now wipe each device concurrently
	wipeGroup := new(errgroup.Group)
	for _, devID := range deviceIDs {
		devID := devID // capture
		wipeGroup.Go(func() error {
			if err := h.fleet.IssueRemoteWipe(ctx, devID); err != nil {
				logger.Error("failed to wipe device",
					zap.String("device_id", devID),
					zap.Error(err),
				)
				atomic.AddInt64(&devicesFailed, 1)
				return err
			}
			atomic.AddInt64(&devicesWiped, 1)
			return nil
		})
	}
	_ = wipeGroup.Wait() // best effort

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
		SessionsTerminated:       sessionsTerminated,
		SessionTerminationError:  sessionError,
		DevicesWiped:             int(devicesWiped),
		DevicesFailed:            int(devicesFailed),
	})
}
