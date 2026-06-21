package handlers

// A3 (FCEX3-7) — Device-identity cookie endpoint.
//
// POST /api/v1/enrollment/device-identity
//
// A browser-facing, unauthenticated endpoint that maps a used enrollment token
// to the Fleet host ID of the device that consumed it, then sets a short-lived
// HTTP-only cookie ("freecloud-device-id") on the response.  The Keycloak
// Authenticator SPI reads this cookie during the browser login flow and passes
// the device ID to POST /api/v1/access/evaluate.
//
// Security note (per spike 0001-conditional-access-authenticator.md §3):
// The cookie value is the Fleet host ID, which is client-visible and therefore
// spoofable.  The threat model accepts this because:
//   - A spoofed host ID will still face the posture check against FleetDM, so
//     the attacker's device must actually pass posture to gain access.
//   - The cookie is bound to the same domain as the Keycloak login page.
//   - The TTL is intentionally short (15 minutes) so stale cookies don't linger.
//
// FAIL-CLOSED: any error → no cookie is set and a 4xx/5xx is returned.
// The Keycloak SPI treats an absent or unresolvable device cookie as a deny
// when a per-app posture policy is configured.

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// deviceIdentityRequest is the JSON body for the device-identity cookie endpoint.
type deviceIdentityRequest struct {
	// EnrollmentToken is the Fleet enrollment token that the device consumed
	// when it enrolled.  It must already have been used (used_at IS NOT NULL)
	// for this endpoint to succeed — the device must have enrolled first.
	EnrollmentToken string `json:"enrollmentToken"`
}

// deviceCookieName is the cookie name read by the Keycloak authenticator SPI.
const deviceCookieName = "freecloud-device-id"

// deviceCookieTTL is intentionally short.  A device that doesn't complete a
// login within 15 minutes must re-trigger the cookie-set step.
const deviceCookieTTL = 15 * time.Minute

// SetDeviceIdentityCookie resolves an enrollment token to the Fleet host ID
// that consumed it and writes a short-lived HTTP-only cookie to the response.
//
// This endpoint is deliberately unauthenticated: the user may not yet have a
// Keycloak session when their device enrolls, and the token itself provides
// adequate proof of enrollment.  It is rate-limited at the route level.
func (h *Handler) SetDeviceIdentityCookie(w http.ResponseWriter, r *http.Request) {
	var req deviceIdentityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.EnrollmentToken == "" {
		respondError(w, http.StatusBadRequest, "enrollmentToken is required")
		return
	}

	if h.db == nil {
		h.logger.Error("device-identity: database not available")
		respondError(w, http.StatusServiceUnavailable, "database not available")
		return
	}

	ctx := r.Context()

	// Look up the host ID recorded when FleetDM called the enrollment callback.
	// used_by_host_id is NULL if the token hasn't been consumed yet (device hasn't
	// enrolled with Fleet), or if the enrollment predates Migration023.
	var hostID *string
	err := h.db.QueryRow(ctx,
		`SELECT used_by_host_id FROM enrollment_tokens
		 WHERE token = $1 AND used_at IS NOT NULL`,
		req.EnrollmentToken,
	).Scan(&hostID)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Token unknown or not yet consumed — device hasn't enrolled yet.
		h.logger.Warn("device-identity: token not found or not yet consumed",
			zap.String("token_prefix", safePrefix(req.EnrollmentToken, 8)))
		respondError(w, http.StatusNotFound, "enrollment token not found or device not yet enrolled")
		return
	case err != nil:
		h.logger.Error("device-identity: DB error looking up token", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	case hostID == nil || *hostID == "":
		// Token was consumed but the host ID wasn't recorded (pre-migration row or
		// race with the callback — device should retry after a moment).
		h.logger.Warn("device-identity: token consumed but no host ID recorded",
			zap.String("token_prefix", safePrefix(req.EnrollmentToken, 8)))
		respondError(w, http.StatusUnprocessableEntity, "device enrollment not yet recorded; retry shortly")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookieName,
		Value:    *hostID,
		Path:     "/",
		MaxAge:   int(deviceCookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,  // Keycloak is served over HTTPS in production
		SameSite: http.SameSiteLaxMode,
	})

	h.logger.Info("device-identity cookie set",
		zap.String("host_id_prefix", safePrefix(*hostID, 8)))

	respondJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"hostId":  *hostID,
		"message": "device identity cookie set; proceed to login",
	})
}

// safePrefix returns the first n characters of s, or all of s if shorter.
// Used to log a partial token/ID without exposing the full value.
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
