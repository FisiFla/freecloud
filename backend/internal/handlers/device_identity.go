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
	"net/url"
	"os"
	"strings"
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

// deviceIdentityTrustedOrigin returns the single origin allowed to call this
// endpoint from a browser: the dashboard's own origin. Read directly from
// CORS_ORIGIN — the same env var main.go already uses to configure the
// API's CORS policy — rather than adding a new config surface; main.go
// falls back to http://localhost:3000 for local dev when it's unset, so
// this mirrors that default exactly.
func deviceIdentityTrustedOrigin() string {
	if origin := os.Getenv("CORS_ORIGIN"); origin != "" {
		return origin
	}
	return "http://localhost:3000"
}

// originAllowed reports whether the request's Origin header (or, failing
// that, the origin parsed from Referer) matches trusted. Fail closed:
// neither header present, either malformed, or either present but
// mismatched is rejected.
func originAllowed(r *http.Request, trusted string) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == trusted
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return false
		}
		return u.Scheme+"://"+u.Host == trusted
	}
	return false
}

// SetDeviceIdentityCookie resolves an enrollment token to the Fleet host ID
// that consumed it and writes a short-lived HTTP-only cookie to the response.
//
// This endpoint is deliberately unauthenticated: the user may not yet have a
// Keycloak session when their device enrolls, and the token itself provides
// adequate proof of enrollment.  It is rate-limited at the route level.
//
// M4: being unauthenticated makes it a CSRF target — a cross-site form POST
// (which browsers send as text/plain or a simple content type, never
// application/json, and without a matching Origin/Referer) could otherwise
// mint a device-identity cookie for an arbitrary visitor. Both checks below
// fail closed.
func (h *Handler) SetDeviceIdentityCookie(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/json") {
		respondError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	if !originAllowed(r, deviceIdentityTrustedOrigin()) {
		h.logger.Warn("device-identity: rejected request with untrusted or missing origin",
			zap.String("origin", r.Header.Get("Origin")), zap.String("referer", r.Header.Get("Referer")))
		respondError(w, http.StatusForbidden, "forbidden: untrusted origin")
		return
	}

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
	// M3: looked up by hash (never plaintext) with the SAME expires_at bound
	// as the original enrollment window — this caps how long after
	// onboarding the cookie-minting capability stays usable at all, even
	// once the device has already enrolled, instead of forever.
	var hostID *string
	err := h.db.QueryRow(ctx,
		`SELECT used_by_host_id FROM enrollment_tokens
		 WHERE token_hash = $1 AND used_at IS NOT NULL AND expires_at > NOW()`,
		enrollmentTokenHash(req.EnrollmentToken),
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
		Secure:   true, // Keycloak is served over HTTPS in production
		SameSite: http.SameSiteLaxMode,
	})

	h.logger.Info("device-identity cookie set",
		zap.String("host_id_prefix", safePrefix(*hostID, 8)))

	respondJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
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
