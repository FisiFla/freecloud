package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

var errEnrollmentTokenNotConsumable = errors.New("enrollment token is unknown, used, or expired")

// enrollmentTokenHash returns the sha256-hex digest of a plaintext Fleet
// enrollment token (M3). enrollment_tokens stores only this hash — never
// the plaintext — matching every other bearer secret in this codebase
// (see api_tokens.go, scim_bearer_tokens). Callers look up a token by
// hashing the value presented to them and comparing against token_hash.
func enrollmentTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EnrollmentCallbackRequest is the body FleetDM POSTs when a host enrolls using
// an enrollment token that was issued for a user during onboarding.
type EnrollmentCallbackRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	HostID          string `json:"host_id"`
	Hostname        string `json:"hostname"`
	OsVersion       string `json:"os_version"`
}

// FleetEnrollmentCallback links an enrolled device to the user its enrollment
// token was issued for, which is what makes the offboarding panic-button able
// to actually wipe the user's devices. It is called by FleetDM, not a browser,
// so it is authenticated by an HMAC-SHA256 signature over the raw request body
// using the shared FLEET_WEBHOOK_SECRET — not a user JWT.
func (h *Handler) FleetEnrollmentCallback(w http.ResponseWriter, r *http.Request) {
	logger := h.logger

	body, err := io.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	if !h.validFleetSignature(body, r.Header.Get("X-Fleet-Signature")) {
		logger.Warn("rejected fleet enrollment callback: bad or missing signature")
		respondError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var req EnrollmentCallbackRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.EnrollmentToken = strings.TrimSpace(req.EnrollmentToken)
	req.HostID = strings.TrimSpace(req.HostID)
	if req.EnrollmentToken == "" || req.HostID == "" {
		respondError(w, http.StatusBadRequest, "enrollment_token and host_id are required")
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	// Link the device to the user atomically: upsert the device, map it to the
	// user, and consume the token so it cannot be replayed.
	ctx := r.Context()
	userID, err := h.linkEnrolledDevice(ctx, req)
	if err != nil {
		if errors.Is(err, errEnrollmentTokenNotConsumable) {
			h.respondEnrollmentTokenState(w, ctx, req.EnrollmentToken)
			return
		}
		logger.Error("failed to link enrolled device", zap.String("host_id", req.HostID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to record device enrollment")
		return
	}

	if auditErr := h.writeAuditEntryBestEffort("fleet-webhook", "device_enroll", "device", req.HostID, map[string]interface{}{
		"host_id":  req.HostID,
		"hostname": req.Hostname,
	}); auditErr != nil {
		logger.Warn("failed to write device-enroll audit log", zap.Error(auditErr))
	}

	logger.Info("linked enrolled device to user",
		zap.String("host_id", req.HostID), zap.String("user_id", userID))
	respondJSON(w, http.StatusOK, map[string]string{"status": "enrolled", "hostId": req.HostID})
}

// linkEnrolledDevice upserts the device, maps it to the user, and consumes the
// enrollment token, all in one transaction.
//
// C2: the token row carries the org it was issued for (onboarding.go
// persistOnboard); this resolves that org_id and sets it EXPLICITLY on both
// the initial INSERT and the ON CONFLICT UPDATE, so a re-enrollment always
// corrects the device's org rather than leaving it on whatever it had
// before (or the schema's Default-Org default, which is exactly the hole
// this fix closes — see requireDeviceInCallerOrg in device_actions.go,
// which trusts devices.org_id completely).
func (h *Handler) linkEnrolledDevice(ctx context.Context, req EnrollmentCallbackRequest) (string, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var userID, orgID string
	if err := tx.QueryRow(ctx,
		`UPDATE enrollment_tokens
		 SET used_at = NOW(), used_by_host_id = $2
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()
		 RETURNING user_id, org_id`,
		enrollmentTokenHash(req.EnrollmentToken), req.HostID,
	).Scan(&userID, &orgID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errEnrollmentTokenNotConsumable
		}
		return "", err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO devices (fleet_host_id, hostname, os_version, org_id, last_seen_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (fleet_host_id) DO UPDATE
		SET hostname = EXCLUDED.hostname, os_version = EXCLUDED.os_version, org_id = EXCLUDED.org_id, last_seen_at = NOW()`,
		req.HostID, req.Hostname, req.OsVersion, orgID,
	); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO users_devices_mapping (user_id, device_id)
		 VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, req.HostID,
	); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
}

func (h *Handler) respondEnrollmentTokenState(w http.ResponseWriter, ctx context.Context, token string) {
	var usedAt *time.Time
	var expiresAt time.Time
	err := h.db.QueryRow(ctx,
		`SELECT used_at, expires_at FROM enrollment_tokens WHERE token_hash = $1`,
		enrollmentTokenHash(token),
	).Scan(&usedAt, &expiresAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		respondError(w, http.StatusNotFound, "unknown enrollment token")
	case err != nil:
		respondError(w, http.StatusInternalServerError, "internal error")
	case usedAt != nil:
		respondError(w, http.StatusConflict, "enrollment token already used")
	case time.Now().After(expiresAt):
		respondError(w, http.StatusGone, "enrollment token expired")
	default:
		respondError(w, http.StatusConflict, "enrollment token could not be consumed")
	}
}

// validFleetSignature constant-time compares the X-Fleet-Signature header (hex
// HMAC-SHA256 of the body, optionally "sha256="-prefixed) against the shared
// secret. An empty secret or header is rejected — fail closed.
func (h *Handler) validFleetSignature(body []byte, header string) bool {
	if h.fleetWebhookSecret == "" || header == "" {
		return false
	}
	header = strings.TrimPrefix(header, "sha256=")
	mac := hmac.New(sha256.New, []byte(h.fleetWebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(header))
}
