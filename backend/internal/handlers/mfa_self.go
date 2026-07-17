package handlers

// B1: MFA self-enrollment endpoints.
//
// These routes allow the authenticated user to manage their OWN MFA factors.
// They are scoped to the calling user's Keycloak sub (portalUserID) — no
// cross-user access is possible.
//
// Endpoints (all under PermSelfService):
//   GET    /api/v1/portal/me/mfa/factors           — list enrolled factors
//   POST   /api/v1/portal/me/mfa/totp/enroll       — trigger TOTP setup
//   POST   /api/v1/portal/me/mfa/webauthn/enroll   — trigger WebAuthn setup
//   DELETE /api/v1/portal/me/mfa/factors/{credId}  — remove a factor
//   POST   /api/v1/portal/me/recovery-codes        — generate/regenerate codes
//   GET    /api/v1/portal/me/recovery-codes        — check code existence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// MFAFactor is a single enrolled MFA credential visible to the calling user.
type MFAFactor struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	CreatedDate int64  `json:"createdDate,omitempty"`
}

// RecoveryCodesResponse is returned after codes are generated.
type RecoveryCodesResponse struct {
	Codes     []string `json:"codes"`
	CreatedAt string   `json:"createdAt"`
}

// ---------------------------------------------------------------------------
// GET /api/v1/portal/me/mfa/factors
// ---------------------------------------------------------------------------

// PortalMyMFAFactors lists the calling user's enrolled MFA credentials.
// Route: GET /api/v1/portal/me/mfa/factors
func (h *Handler) PortalMyMFAFactors(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}

	creds, err := h.keycloak.GetUserCredentialsFull(r.Context(), uid)
	if err != nil {
		h.logger.Error("mfa: failed to list factors", zap.String("user_id", uid), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to list MFA factors")
		return
	}

	factors := []MFAFactor{}
	for _, c := range creds {
		if c.Type == nil {
			continue
		}
		t := *c.Type
		// Only surface MFA credential types.
		if t != "otp" && t != "webauthn" {
			continue
		}
		f := MFAFactor{Type: t}
		if c.ID != nil {
			f.ID = *c.ID
		}
		if c.CreatedDate != nil {
			f.CreatedDate = *c.CreatedDate
		}
		factors = append(factors, f)
	}

	// Update coverage cache best-effort so analytics stay accurate.
	if h.db != nil {
		hasMFA := len(factors) > 0
		go h.updateMFACoverageCache(uid, hasMFA)
	}

	respondJSON(w, http.StatusOK, factors)
}

// ---------------------------------------------------------------------------
// POST /api/v1/portal/me/mfa/totp/enroll
// ---------------------------------------------------------------------------

// PortalEnrollTOTP triggers the Keycloak CONFIGURE_TOTP required action for
// the calling user. On next login Keycloak shows the TOTP QR code.
// Route: POST /api/v1/portal/me/mfa/totp/enroll
func (h *Handler) PortalEnrollTOTP(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}

	const action = "CONFIGURE_TOTP"
	if err := h.keycloak.SetRequiredAction(r.Context(), uid, action); err != nil {
		h.logger.Error("mfa: failed to set CONFIGURE_TOTP", zap.String("user_id", uid), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to initiate TOTP enrollment")
		return
	}

	if h.db != nil {
		actorID := middleware.GetActorID(r.Context())
		if auditErr := h.writeAuditEntryBestEffort(actorID, "mfa_totp_enroll_requested", "user", uid, map[string]interface{}{
			"action": action,
		}); auditErr != nil {
			h.logger.Warn("mfa: failed to write audit log", zap.Error(auditErr))
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"userId":  uid,
		"action":  action,
		"pending": true,
		"message": "TOTP enrollment initiated. Complete setup on next login.",
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/portal/me/mfa/webauthn/enroll
// ---------------------------------------------------------------------------

// PortalEnrollWebAuthn triggers the webauthn-register required action for the
// calling user. On next login Keycloak presents the passkey registration flow.
// Route: POST /api/v1/portal/me/mfa/webauthn/enroll
func (h *Handler) PortalEnrollWebAuthn(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}

	const action = "webauthn-register"
	if err := h.keycloak.SetRequiredAction(r.Context(), uid, action); err != nil {
		h.logger.Error("mfa: failed to set webauthn-register", zap.String("user_id", uid), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to initiate WebAuthn enrollment")
		return
	}

	if h.db != nil {
		actorID := middleware.GetActorID(r.Context())
		if auditErr := h.writeAuditEntryBestEffort(actorID, "mfa_webauthn_enroll_requested", "user", uid, map[string]interface{}{
			"action": action,
		}); auditErr != nil {
			h.logger.Warn("mfa: failed to write audit log", zap.Error(auditErr))
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"userId":  uid,
		"action":  action,
		"pending": true,
		"message": "WebAuthn enrollment initiated. Complete registration on next login.",
	})
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/portal/me/mfa/factors/{credId}
// ---------------------------------------------------------------------------

// PortalRemoveMFAFactor removes one of the calling user's MFA credentials.
// Only "otp" and "webauthn" credentials may be removed via this endpoint.
// Route: DELETE /api/v1/portal/me/mfa/factors/{credId}
func (h *Handler) PortalRemoveMFAFactor(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}

	credID := chi.URLParam(r, "credId")
	if err := ValidateOpaqueID(credID, "credId"); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Verify ownership: credential must belong to caller and be an MFA type.
	creds, err := h.keycloak.GetUserCredentialsFull(r.Context(), uid)
	if err != nil {
		h.logger.Error("mfa: failed to verify credential ownership", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to verify credential")
		return
	}
	found := false
	remainingAfter := 0
	for _, c := range creds {
		if c.Type == nil {
			continue
		}
		if c.ID != nil && *c.ID == credID {
			if *c.Type == "otp" || *c.Type == "webauthn" {
				found = true
			}
			continue // don't count this one in remainingAfter
		}
		if *c.Type == "otp" || *c.Type == "webauthn" {
			remainingAfter++
		}
	}
	if !found {
		respondError(w, http.StatusNotFound, "MFA credential not found")
		return
	}

	if err := h.keycloak.DeleteCredential(r.Context(), uid, credID); err != nil {
		h.logger.Error("mfa: failed to delete credential",
			zap.String("user_id", uid), zap.String("cred_id", credID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to remove MFA factor")
		return
	}

	if h.db != nil {
		actorID := middleware.GetActorID(r.Context())
		if auditErr := h.writeAuditEntryBestEffort(actorID, "mfa_factor_removed", "user", uid, map[string]interface{}{
			"credential_id": credID,
		}); auditErr != nil {
			h.logger.Warn("mfa: failed to write audit log", zap.Error(auditErr))
		}
		go h.updateMFACoverageCache(uid, remainingAfter > 0)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"removed":      true,
		"credentialId": credID,
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/portal/me/recovery-codes
// ---------------------------------------------------------------------------

// PortalGenerateRecoveryCodes generates a new set of single-use recovery codes
// for the calling user. Existing codes are replaced atomically. The plain-text
// codes are returned once and never stored — only SHA-256 hashes are persisted.
// Route: POST /api/v1/portal/me/recovery-codes
func (h *Handler) PortalGenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	const codeCount = 10
	plain := make([]string, codeCount)
	hashed := make([]string, codeCount)
	for i := range plain {
		c, err := generateRecoveryCode()
		if err != nil {
			h.logger.Error("mfa: failed to generate recovery code", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "failed to generate recovery codes")
			return
		}
		plain[i] = c
		hashed[i] = hashRecoveryCode(c)
	}

	ctx := r.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.logger.Error("mfa: failed to begin transaction", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		`DELETE FROM mfa_recovery_codes WHERE user_id = $1`, uid,
	); err != nil {
		h.logger.Error("mfa: failed to delete old recovery codes", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	for _, codeHash := range hashed {
		if _, execErr := tx.Exec(ctx,
			`INSERT INTO mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)`,
			uid, codeHash,
		); execErr != nil {
			h.logger.Error("mfa: failed to insert recovery code", zap.Error(execErr))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("mfa: failed to commit recovery codes", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	actorID := middleware.GetActorID(r.Context())
	if auditErr := h.writeAuditEntryBestEffort(actorID, "mfa_recovery_codes_generated", "user", uid, map[string]interface{}{
		"count": codeCount,
	}); auditErr != nil {
		h.logger.Warn("mfa: failed to write audit log", zap.Error(auditErr))
	}

	respondJSON(w, http.StatusOK, RecoveryCodesResponse{
		Codes:     plain,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/portal/me/recovery-codes
// ---------------------------------------------------------------------------

// PortalRecoveryCodesStatus reports whether the calling user has unused
// recovery codes stored (without returning them).
// Route: GET /api/v1/portal/me/recovery-codes
func (h *Handler) PortalRecoveryCodesStatus(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	var count int
	err := h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, uid,
	).Scan(&count)
	if err != nil {
		h.logger.Error("mfa: failed to count recovery codes", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"hasRecoveryCodes":   count > 0,
		"remainingCodeCount": count,
	})
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// generateRecoveryCode returns a cryptographically random 10-char hex string.
func generateRecoveryCode() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// hashRecoveryCode returns the hex-encoded SHA-256 hash of a recovery code.
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// updateMFACoverageCache upserts the user's MFA enrollment state in the
// local coverage cache table. Called best-effort from self-service endpoints
// so the analytics snapshot reflects near-real-time enrollment data.
func (h *Handler) updateMFACoverageCache(userID string, hasMFA bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.db.Exec(ctx,
		`INSERT INTO mfa_coverage_cache (user_id, has_mfa, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (user_id) DO UPDATE SET has_mfa = EXCLUDED.has_mfa, updated_at = NOW()`,
		userID, hasMFA,
	); err != nil {
		zap.L().Warn("mfa: failed to update coverage cache",
			zap.String("user_id", userID), zap.Error(err))
	}
}

// CheckAndConsumeRecoveryCode verifies and marks a recovery code as used.
// Returns true if a valid unused code was found and consumed. Exported for use
// in tests.
func CheckAndConsumeRecoveryCode(ctx context.Context, db DBPool, userID, code string) (bool, error) {
	hash := hashRecoveryCode(code)
	var id string
	err := db.QueryRow(ctx,
		`UPDATE mfa_recovery_codes SET used_at = NOW()
		 WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
		 RETURNING id::text`,
		userID, hash,
	).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
