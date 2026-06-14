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

	ctx := r.Context()

	// Resolve the token to its user, checking it is unused and unexpired.
	var userID string
	var usedAt *time.Time
	var expiresAt time.Time
	lookupErr := h.db.QueryRow(ctx,
		`SELECT user_id, used_at, expires_at FROM enrollment_tokens WHERE token = $1`,
		req.EnrollmentToken,
	).Scan(&userID, &usedAt, &expiresAt)
	switch {
	case errors.Is(lookupErr, pgx.ErrNoRows):
		respondError(w, http.StatusNotFound, "unknown enrollment token")
		return
	case lookupErr != nil:
		logger.Error("enrollment token lookup failed", zap.Error(lookupErr))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if usedAt != nil {
		respondError(w, http.StatusConflict, "enrollment token already used")
		return
	}
	if time.Now().After(expiresAt) {
		respondError(w, http.StatusGone, "enrollment token expired")
		return
	}

	// Link the device to the user atomically: upsert the device, map it to the
	// user, and consume the token so it cannot be replayed.
	if err := h.linkEnrolledDevice(ctx, req, userID); err != nil {
		logger.Error("failed to link enrolled device", zap.String("host_id", req.HostID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to record device enrollment")
		return
	}

	// Audit on a detached context so a disconnect can't drop the record.
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	details, _ := json.Marshal(map[string]interface{}{"host_id": req.HostID, "hostname": req.Hostname})
	if _, auditErr := h.db.Exec(auditCtx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		"fleet-webhook", "device_enroll", "device", req.HostID, string(details),
	); auditErr != nil {
		logger.Warn("failed to write device-enroll audit log", zap.Error(auditErr))
	}

	logger.Info("linked enrolled device to user",
		zap.String("host_id", req.HostID), zap.String("user_id", userID))
	respondJSON(w, http.StatusOK, map[string]string{"status": "enrolled", "hostId": req.HostID})
}

// linkEnrolledDevice upserts the device, maps it to the user, and consumes the
// enrollment token, all in one transaction.
func (h *Handler) linkEnrolledDevice(ctx context.Context, req EnrollmentCallbackRequest, userID string) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO devices (fleet_host_id, hostname, os_version, last_seen_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (fleet_host_id) DO UPDATE
		 SET hostname = EXCLUDED.hostname, os_version = EXCLUDED.os_version, last_seen_at = NOW()`,
		req.HostID, req.Hostname, req.OsVersion,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO users_devices_mapping (user_id, device_id)
		 VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, req.HostID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE enrollment_tokens SET used_at = NOW() WHERE token = $1`,
		req.EnrollmentToken,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
