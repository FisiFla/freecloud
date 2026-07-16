package handlers

// D2 — Policy preview / test endpoint.
//
// POST /api/v1/apps/{appId}/policy/preview
//
// Evaluates a HYPOTHETICAL access scenario: given a device, client IP, and
// time, returns what the policy decision would be. This is a dry-run: it does
// NOT write an audit log and does NOT fire any notifications.
//
// Gated by PermManagePolicies. Registered in the mutateLimiter group.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// PolicyPreviewRequest is the JSON body for the preview endpoint.
type PolicyPreviewRequest struct {
	// DeviceID is optional. If provided, posture is checked for this device;
	// otherwise posture checks are skipped.
	DeviceID string `json:"deviceId,omitempty"`
	// ClientIP is the hypothetical originating client IP for network/geo conditions.
	ClientIP string `json:"clientIp,omitempty"`
	// EvalTime is an RFC3339 timestamp to use as "now" for the time-window check.
	// If empty, the current server time (UTC) is used.
	EvalTime string `json:"evalTime,omitempty"`
	// UserID is optional; needed only when DeviceID is supplied (to verify the
	// device-user mapping). May be omitted to skip device/posture checks entirely.
	UserID string `json:"userId,omitempty"`
}

// PreviewAppPolicy evaluates a hypothetical access scenario against the app's
// current policy and returns allow/deny + reasons without persisting anything.
func (h *Handler) PreviewAppPolicy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		respondError(w, http.StatusBadRequest, "appId is required")
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	if !h.requireAppInCallerOrg(w, r, appID) {
		return
	}

	var req PolicyPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Determine evaluation time.
	evalTime := time.Now().UTC()
	if req.EvalTime != "" {
		t, err := time.Parse(time.RFC3339, req.EvalTime)
		if err != nil {
			respondError(w, http.StatusBadRequest, "evalTime must be RFC3339 (e.g. 2025-01-01T12:00:00Z)")
			return
		}
		evalTime = t.UTC()
	}

	// Determine client IP.
	clientIP := strings.TrimSpace(req.ClientIP)
	if clientIP == "" {
		clientIP = "0.0.0.0" // safe default for preview; no network condition will match
	}

	ctx := r.Context()

	// Load policy for this app.
	policy := appPolicy{}
	{
		var reqEnrolled, reqDisk, reqVulns bool
		var maxOsAgeDays *int
		var allowedTimeStart, allowedTimeEnd *string
		var networkAllowlist, geoCountryAllowlist []string
		err := h.db.QueryRow(ctx,
			`SELECT p.require_enrolled, p.require_disk_encrypted, p.require_no_critical_vulns,
			        p.max_os_age_days,
			        p.allowed_time_start, p.allowed_time_end,
			        p.network_allowlist, p.geo_country_allowlist
			 FROM app_access_policies p WHERE p.app_id = $1`,
			appID,
		).Scan(&reqEnrolled, &reqDisk, &reqVulns, &maxOsAgeDays,
			&allowedTimeStart, &allowedTimeEnd,
			&networkAllowlist, &geoCountryAllowlist)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			h.logger.Error("policy preview: failed to load policy", zap.String("app_id", appID), zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if err == nil {
			policy = appPolicy{
				RequireEnrolled:        reqEnrolled,
				RequireDiskEncrypted:   reqDisk,
				RequireNoCriticalVulns: reqVulns,
				MaxOsAgeDays:           maxOsAgeDays,
				AllowedTimeStart:       allowedTimeStart,
				AllowedTimeEnd:         allowedTimeEnd,
				NetworkAllowlist:       networkAllowlist,
				GeoCountryAllowlist:    geoCountryAllowlist,
			}
		}
		// ErrNoRows → zero-value policy (no requirements).
	}

	var reasons []string

	// Optional posture check when a deviceId + userId are supplied.
	if req.DeviceID != "" && req.UserID != "" {
		// Verify device-user mapping.
		var mappedDeviceID string
		err := h.db.QueryRow(ctx,
			`SELECT device_id FROM users_devices_mapping WHERE user_id = $1 AND device_id = $2`,
			req.UserID, req.DeviceID,
		).Scan(&mappedDeviceID)
		if err != nil {
			reasons = append(reasons, "device is not enrolled for user")
		} else {
			reasons = append(reasons, evaluateDevicePosture(ctx, h, policy, req.DeviceID)...)
		}
	}

	// Evaluate D1 conditions.
	condReasons := evalConditions(policy, clientIP, evalTime, h.geoIP)
	reasons = append(reasons, condReasons...)

	// Dry-run: no audit log, no notification.
	respondJSON(w, http.StatusOK, AccessEvalResponse{
		Allow:   len(reasons) == 0,
		Reasons: reasons,
	})
}
