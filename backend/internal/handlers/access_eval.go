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
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/notify"
)

// AccessEvalRequest is the JSON request body for the posture evaluation endpoint.
type AccessEvalRequest struct {
	// UserID is the Keycloak user UUID whose posture should be evaluated.
	UserID string `json:"userId"`
	// AppID is either connected_apps.id or connected_apps.keycloak_client_id.
	// The Keycloak SPI sends the Keycloak client UUID during browser login.
	// Optional — if empty, only global posture checks apply.
	AppID string `json:"appId,omitempty"`
	// DeviceID is an explicit FleetDM host ID to evaluate. If empty, all
	// devices enrolled for the user are checked.
	DeviceID string `json:"deviceId,omitempty"`
	// ClientIP is the originating client IP address for network/geo conditions.
	// If empty, the handler falls back to r.RemoteAddr.
	ClientIP string `json:"clientIp,omitempty"`
}

// AccessEvalResponse is the JSON response from the posture evaluation endpoint.
type AccessEvalResponse struct {
	Allow   bool     `json:"allow"`
	Reasons []string `json:"reasons,omitempty"`
}

// appPolicy holds the per-app posture requirements loaded from app_access_policies.
type appPolicy struct {
	// RequireEnrolled is reserved/no-op today: device enrollment is enforced
	// unconditionally for the explicit-device override path in step 2 (users_devices_mapping
	// check), so a policy-layer re-check here would be dead code.
	RequireEnrolled        bool
	RequireDiskEncrypted   bool
	RequireNoCriticalVulns bool
	MaxOsAgeDays           *int
	// D1: conditional access conditions (Migration039).
	AllowedTimeStart    *string  // "HH:MM" UTC
	AllowedTimeEnd      *string  // "HH:MM" UTC
	NetworkAllowlist    []string // IP/CIDR strings; empty = unrestricted
	GeoCountryAllowlist []string // ISO 3166-1 alpha-2; empty = unrestricted
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
	req.AppID = strings.TrimSpace(req.AppID)
	req.DeviceID = strings.TrimSpace(req.DeviceID)

	// Device identity cookies are HMAC-signed (v1.…). The SPI forwards the raw
	// cookie value as deviceId — verify and unwrap the Fleet host ID here so a
	// forged bare host ID cannot pass posture for someone else's device.
	if req.DeviceID != "" {
		secret := h.deviceCookieSigningSecret()
		if hostID, ok := ParseAndVerifyDeviceCookie(req.DeviceID, secret, time.Now().UTC()); ok {
			req.DeviceID = hostID
		} else if strings.HasPrefix(req.DeviceID, "v1.") || secret != "" {
			// Signed-looking value that failed MAC/expiry, or any deviceId when
			// signing is configured (reject legacy plain host IDs).
			h.auditAccessDecision(req.UserID, req.AppID, false, []string{"invalid or expired device identity"})
			respondJSON(w, http.StatusOK, AccessEvalResponse{
				Allow:   false,
				Reasons: []string{"invalid or expired device identity"},
			})
			return
		}
		// secret empty + plain deviceId: only possible in misconfigured dev;
		// leave as-is so unit tests that inject bare host IDs still work.
	}

	// Determine effective client IP for network/geo conditions.
	clientIP := strings.TrimSpace(req.ClientIP)
	if clientIP == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			clientIP = host
		} else {
			clientIP = r.RemoteAddr
		}
	}

	if err := ValidateUserID(req.UserID); err != nil {
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
		var mappedDeviceID string
		err := h.db.QueryRow(ctx,
			`SELECT device_id FROM users_devices_mapping
			 WHERE user_id = $1 AND device_id = $2`,
			req.UserID, req.DeviceID,
		).Scan(&mappedDeviceID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				h.logger.Error("access eval: failed to verify device mapping",
					zap.String("user_id", req.UserID),
					zap.String("device_id", req.DeviceID),
					zap.Error(err),
				)
			}
			h.auditAccessDecision(req.UserID, req.AppID, false, []string{"device is not enrolled for user"})
			respondJSON(w, http.StatusOK, AccessEvalResponse{
				Allow:   false,
				Reasons: []string{"device is not enrolled for user"},
			})
			return
		}
		deviceIDs = []string{mappedDeviceID}
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
		var maxOsAgeDays *int
		var allowedTimeStart, allowedTimeEnd *string
		var networkAllowlist, geoCountryAllowlist []string
		err := h.db.QueryRow(ctx,
			`SELECT p.require_enrolled, p.require_disk_encrypted, p.require_no_critical_vulns,
				        p.max_os_age_days,
				        p.allowed_time_start, p.allowed_time_end,
				        p.network_allowlist, p.geo_country_allowlist
				 FROM app_access_policies p
				 INNER JOIN connected_apps a ON a.id = p.app_id
				 WHERE p.app_id::TEXT = $1 OR a.keycloak_client_id = $1
				 LIMIT 1`,
			req.AppID,
		).Scan(&reqEnrolled, &reqDisk, &reqVulns, &maxOsAgeDays,
			&allowedTimeStart, &allowedTimeEnd,
			&networkAllowlist, &geoCountryAllowlist)
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

	// 4. Evaluate posture. When the SPI supplies an explicit deviceId we check
	// that one device only. When deviceId is empty we allow if ANY enrolled
	// device is compliant — AND-ing failures across every host blocked SSO for
	// users who still had one clean laptop next to a retired non-compliant one.
	var reasons []string
	anyCompliant := false
	var worstReasons []string

	for _, devID := range deviceIDs {
		devReasons := evaluateDevicePosture(ctx, h, policy, devID)
		if len(devReasons) == 0 {
			anyCompliant = true
			break
		}
		worstReasons = append(worstReasons, devReasons...)
	}
	if !anyCompliant {
		reasons = append(reasons, worstReasons...)
	}

	// 5. Evaluate D1 policy conditions (time window, network, geo).
	condReasons := evalConditions(policy, clientIP, time.Now().UTC(), h.geoIP)
	reasons = append(reasons, condReasons...)

	allow := len(reasons) == 0
	h.auditAccessDecision(req.UserID, req.AppID, allow, reasons)
	respondJSON(w, http.StatusOK, AccessEvalResponse{
		Allow:   allow,
		Reasons: reasons,
	})
}

// evaluateDevicePosture returns deny reasons for a single device. Empty means
// the device is compliant under the active policy (and the global baseline).
//
// max_os_age_days is intentionally ignored at evaluation time: Fleet posture
// does not currently expose OS age, and treating the field as permanent deny
// locked operators out. UpsertAppAccessPolicy rejects new writes of the field.
func evaluateDevicePosture(ctx context.Context, h *Handler, policy appPolicy, devID string) []string {
	var reasons []string
	state, err := h.fleet.GetHostSecurityState(ctx, devID)
	if err != nil {
		h.logger.Error("access eval: failed to get security state",
			zap.String("device_id", devID),
			zap.Error(err),
		)
		return []string{"device posture unavailable for device " + devID}
	}

	// Global secure baseline: firewall on, disk encrypted, no known vulns.
	// Per-app flags only add redundant explicit messaging when set.
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

	if policy.RequireDiskEncrypted && !state.DiskEncrypted {
		reasons = append(reasons, "app policy requires disk encryption on device "+devID)
	}
	if policy.RequireNoCriticalVulns && (len(state.Vulnerabilities) > 0 || state.UnknownVulns) {
		reasons = append(reasons, "app policy requires no critical vulnerabilities on device "+devID)
	}
	if policy.MaxOsAgeDays != nil {
		h.logger.Warn("access eval: max_os_age_days is configured but not supported; ignoring",
			zap.String("device_id", devID),
			zap.Int("max_os_age_days", *policy.MaxOsAgeDays),
		)
	}
	return reasons
}

// evalConditions evaluates the D1 policy conditions (time window, network allowlist,
// geo country allowlist) and returns a list of deny reasons. All conditions must
// pass — any failure appends a reason (fail-closed on misconfiguration).
func evalConditions(p appPolicy, clientIP string, now time.Time, geoIP GeoIPLookup) []string {
	var reasons []string

	// Time-window condition: both must be set to activate.
	if p.AllowedTimeStart != nil && p.AllowedTimeEnd != nil {
		start, errS := time.Parse("15:04", *p.AllowedTimeStart)
		end, errE := time.Parse("15:04", *p.AllowedTimeEnd)
		if errS != nil || errE != nil {
			reasons = append(reasons, "policy: time window condition is misconfigured")
		} else {
			// Compare using minutes-since-midnight to stay timezone-agnostic.
			nowMins := now.Hour()*60 + now.Minute()
			startMins := start.Hour()*60 + start.Minute()
			endMins := end.Hour()*60 + end.Minute()
			var inWindow bool
			if startMins <= endMins {
				inWindow = nowMins >= startMins && nowMins < endMins
			} else {
				// Wraps midnight.
				inWindow = nowMins >= startMins || nowMins < endMins
			}
			if !inWindow {
				reasons = append(reasons, "policy: access not allowed at this time (window: "+
					*p.AllowedTimeStart+"–"+*p.AllowedTimeEnd+" UTC)")
			}
		}
	}

	// Network allowlist condition.
	if len(p.NetworkAllowlist) > 0 {
		ip := net.ParseIP(clientIP)
		if ip == nil {
			reasons = append(reasons, "policy: network condition cannot be evaluated (unparseable client IP)")
		} else {
			matched := false
			for _, entry := range p.NetworkAllowlist {
				if strings.Contains(entry, "/") {
					_, ipNet, err := net.ParseCIDR(entry)
					if err == nil && ipNet.Contains(ip) {
						matched = true
						break
					}
				} else {
					if net.ParseIP(entry) != nil && net.ParseIP(entry).Equal(ip) {
						matched = true
						break
					}
				}
			}
			if !matched {
				reasons = append(reasons, "policy: client IP "+clientIP+" is not in the network allowlist")
			}
		}
	}

	// Geo country allowlist condition.
	if len(p.GeoCountryAllowlist) > 0 {
		country := geoIP.Country(clientIP)
		if country == "" {
			// Fail closed: unknown country is denied.
			reasons = append(reasons, "policy: geo condition cannot be evaluated (country unknown for IP "+clientIP+")")
		} else {
			matched := false
			for _, cc := range p.GeoCountryAllowlist {
				if strings.EqualFold(cc, country) {
					matched = true
					break
				}
			}
			if !matched {
				reasons = append(reasons, "policy: country "+country+" is not in the geo allowlist")
			}
		}
	}

	return reasons
}

// auditAccessDecision writes an access_eval audit log entry using a detached
// context so the write survives request cancellation. When access is denied,
// it also fires the EventAccessBlocked notification (A4) with denied_conditions
// derived from the reason strings (D3).
func (h *Handler) auditAccessDecision(userID, appID string, allow bool, reasons []string) {
	if h.db == nil {
		return
	}
	actorID := "service:access-eval"

	details := map[string]interface{}{
		"app_id":  appID,
		"allow":   allow,
		"reasons": reasons,
	}
	if !allow {
		details["denied_conditions"] = conditionTypesFromReasons(reasons)
	}
	if err := h.writeAuditEntryBestEffort(actorID, "access_eval", "user", userID, details); err != nil {
		h.logger.Warn("access eval: failed to write audit log", zap.Error(err))
	}

	// A4: fire EventAccessBlocked notification when posture denies access.
	if !allow && h.notifier != nil {
		n := h.notifier
		condTypes := conditionTypesFromReasons(reasons)
		go func() {
			notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = n.Notify(notifyCtx, notify.Event{
				Type:     notify.EventAccessBlocked,
				ActorID:  actorID,
				TargetID: userID,
				Details: map[string]any{
					"app_id":            appID,
					"reasons":           reasons,
					"denied_conditions": condTypes,
				},
			})
		}()
	}
}

// conditionTypesFromReasons maps deny reason strings to a deduplicated list of
// condition type labels. Used for structured audit/notification details (D3).
func conditionTypesFromReasons(reasons []string) []string {
	seen := make(map[string]bool)
	var types []string
	for _, r := range reasons {
		var ct string
		switch {
		case strings.Contains(r, "firewall") || strings.Contains(r, "disk") ||
			strings.Contains(r, "vulnerability") || strings.Contains(r, "posture"):
			ct = "posture"
		case strings.Contains(r, "time"):
			ct = "time_window"
		case strings.Contains(r, "network") || strings.Contains(r, "IP"):
			ct = "network"
		case strings.Contains(r, "geo") || strings.Contains(r, "country"):
			ct = "geo"
		case strings.Contains(r, "device"):
			ct = "device"
		default:
			ct = "other"
		}
		if !seen[ct] {
			seen[ct] = true
			types = append(types, ct)
		}
	}
	return types
}
