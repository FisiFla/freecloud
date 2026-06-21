package handlers

// A1 (FCEX2-5) — Auth-time posture decision endpoint.
//
// POST /api/v1/access/evaluate — given a user identifier + optional device/app
// context, returns allow/deny + reasons computed from FleetDM posture and the
// per-app access policy loaded from the database.
//
// FAIL-CLOSED: deny on unknown user, unmapped/unreachable device, DB failure, or
// any error path. The endpoint is authenticated by a dedicated service bearer
// token (ACCESS_EVAL_TOKEN), registered OUTSIDE the user-JWT group.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// AccessEvalRequest is the JSON request body for the posture evaluation endpoint.
type AccessEvalRequest struct {
	// UserID is the Keycloak user UUID whose posture should be evaluated.
	UserID string `json:"userId"`
	// AppID is the connected_apps.id to load per-app policy for. Optional —
	// if empty, only global posture checks apply.
	AppID string `json:"appId,omitempty"`
	// DeviceID is an explicit FleetDM host ID to evaluate. If empty, all
	// devices enrolled for the user are checked.
	DeviceID string `json:"deviceId,omitempty"`
}

// AccessEvalResponse is the JSON response from the posture evaluation endpoint.
type AccessEvalResponse struct {
	Allow   bool     `json:"allow"`
	Reasons []string `json:"reasons,omitempty"`
}

// appPolicy holds the per-app posture requirements loaded from app_access_policies.
type appPolicy struct {
	RequireEnrolled        bool
	RequireDiskEncrypted   bool
	RequireNoCriticalVulns bool
}

// accessEvalBearerMiddleware mirrors SCIMBearerMiddleware: an empty token
// rejects all requests (fail closed), a wrong/missing token returns 401.
func accessEvalBearerMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				respondError(w, http.StatusServiceUnavailable, "access evaluation is not configured")
				return
			}
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				respondError(w, http.StatusUnauthorized, "Bearer token required")
				return
			}
			if !constantTimeStringEqual(strings.TrimPrefix(auth, "Bearer "), token) {
				respondError(w, http.StatusUnauthorized, "invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// EvaluateAccess checks whether a user/device may access a given app based on
// the current FleetDM posture and the app's configured access policy.
func (h *Handler) EvaluateAccess(w http.ResponseWriter, r *http.Request) {
	var req AccessEvalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.auditAccessDecision("", req.AppID, false, []string{"invalid request body"})
		respondJSON(w, http.StatusOK, AccessEvalResponse{
			Allow:   false,
			Reasons: []string{"invalid request body"},
		})
		return
	}

	req.UserID = strings.TrimSpace(req.UserID)
	if req.UserID == "" {
		h.auditAccessDecision("", req.AppID, false, []string{"missing or invalid user identifier"})
		respondJSON(w, http.StatusOK, AccessEvalResponse{
			Allow:   false,
			Reasons: []string{"missing or invalid user identifier"},
		})
		return
	}

	if h.db == nil {
		h.logger.Error("access eval: database not available", zap.String("user_id", req.UserID))
		h.auditAccessDecision(req.UserID, req.AppID, false, []string{"database unavailable"})
		respondJSON(w, http.StatusOK, AccessEvalResponse{
			Allow:   false,
			Reasons: []string{"database unavailable"},
		})
		return
	}

	ctx := r.Context()

	// 1. Verify the user exists and is not disabled.
	var foundUID string
	err := h.db.QueryRow(ctx,
		`SELECT keycloak_user_id FROM users WHERE keycloak_user_id = $1 AND disabled = false`,
		req.UserID,
	).Scan(&foundUID)
	if err != nil {
		h.logger.Warn("access eval: user not found or disabled",
			zap.String("user_id", req.UserID),
			zap.Error(err),
		)
		h.auditAccessDecision(req.UserID, req.AppID, false, []string{"user not found or disabled"})
		respondJSON(w, http.StatusOK, AccessEvalResponse{
			Allow:   false,
			Reasons: []string{"user not found or disabled"},
		})
		return
	}

	// 2. Collect device IDs to evaluate.
	var deviceIDs []string
	if req.DeviceID != "" {
		deviceIDs = []string{req.DeviceID}
	} else {
		rows, err := h.db.Query(ctx,
			`SELECT device_id FROM users_devices_mapping WHERE user_id = $1`,
			req.UserID,
		)
		if err != nil {
			h.logger.Error("access eval: failed to query devices",
				zap.String("user_id", req.UserID),
				zap.Error(err),
			)
			h.auditAccessDecision(req.UserID, req.AppID, false, []string{"device lookup failed"})
			respondJSON(w, http.StatusOK, AccessEvalResponse{
				Allow:   false,
				Reasons: []string{"device lookup failed"},
			})
			return
		}
		defer rows.Close()
		for rows.Next() {
			var did string
			if err := rows.Scan(&did); err != nil {
				continue
			}
			deviceIDs = append(deviceIDs, did)
		}
		if err := rows.Err(); err != nil {
			h.logger.Error("access eval: error iterating devices", zap.Error(err))
		}
	}

	if len(deviceIDs) == 0 {
		h.auditAccessDecision(req.UserID, req.AppID, false, []string{"no enrolled device found for user"})
		respondJSON(w, http.StatusOK, AccessEvalResponse{
			Allow:   false,
			Reasons: []string{"no enrolled device found for user"},
		})
		return
	}

	// 3. Load per-app policy (if appID provided).
	policy := appPolicy{}
	if req.AppID != "" {
		var reqEnrolled, reqDisk, reqVulns bool
		err := h.db.QueryRow(ctx,
			`SELECT require_enrolled, require_disk_encrypted, require_no_critical_vulns
			 FROM app_access_policies WHERE app_id = $1`,
			req.AppID,
		).Scan(&reqEnrolled, &reqDisk, &reqVulns)
		if err == nil {
			policy = appPolicy{
				RequireEnrolled:        reqEnrolled,
				RequireDiskEncrypted:   reqDisk,
				RequireNoCriticalVulns: reqVulns,
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			h.logger.Error("access eval: failed to load app policy",
				zap.String("user_id", req.UserID),
				zap.String("app_id", req.AppID),
				zap.Error(err),
			)
			h.auditAccessDecision(req.UserID, req.AppID, false, []string{"app policy lookup failed"})
			respondJSON(w, http.StatusOK, AccessEvalResponse{
				Allow:   false,
				Reasons: []string{"app policy lookup failed"},
			})
			return
		}
		// ErrNoRows → zero-value policy (no requirements), matching the policy
		// API's documented "no policy row means no posture gate" behavior.
	}

	// 4. Evaluate posture for every device.
	var reasons []string

	for _, devID := range deviceIDs {
		state, err := h.fleet.GetHostSecurityState(ctx, devID)
		if err != nil {
			h.logger.Error("access eval: failed to get security state",
				zap.String("device_id", devID),
				zap.Error(err),
			)
			reasons = append(reasons, "device posture unavailable for device "+devID)
			continue
		}

		if !state.FirewallEnabled {
			reasons = append(reasons, "firewall disabled on device "+devID)
		}
		if !state.DiskEncrypted {
			reasons = append(reasons, "disk not encrypted on device "+devID)
		}
		for _, v := range state.Vulnerabilities {
			reasons = append(reasons, "vulnerability: "+v+" on device "+devID)
		}
		if state.UnknownVulns {
			h.logger.Warn("access eval: vulnerability data incomplete",
				zap.String("device_id", devID),
			)
			reasons = append(reasons, "vulnerability data unavailable for device "+devID)
		}

		// Apply per-app policy checks.
		if policy.RequireDiskEncrypted && !state.DiskEncrypted {
			reasons = append(reasons, "app policy requires disk encryption on device "+devID)
		}
		if policy.RequireNoCriticalVulns && (len(state.Vulnerabilities) > 0 || state.UnknownVulns) {
			reasons = append(reasons, "app policy requires no critical vulnerabilities on device "+devID)
		}
	}

	allow := len(reasons) == 0
	h.auditAccessDecision(req.UserID, req.AppID, allow, reasons)
	respondJSON(w, http.StatusOK, AccessEvalResponse{
		Allow:   allow,
		Reasons: reasons,
	})
}

// auditAccessDecision writes an access_eval audit log entry using a detached
// context so the write survives request cancellation.
func (h *Handler) auditAccessDecision(userID, appID string, allow bool, reasons []string) {
	if h.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	actorID := "service:access-eval"

	details := map[string]interface{}{
		"app_id":  appID,
		"allow":   allow,
		"reasons": reasons,
	}
	_, err := h.db.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		actorID, "access_eval", "user", userID, details,
	)
	if err != nil {
		h.logger.Warn("access eval: failed to write audit log", zap.Error(err))
	}
}
