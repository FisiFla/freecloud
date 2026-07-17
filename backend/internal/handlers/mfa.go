package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// MFAStatusResponse describes the MFA enrollment state for a user.
type MFAStatusResponse struct {
	UserID          string `json:"userId"`
	OTPEnabled      bool   `json:"otpEnabled"`
	OTPPending      bool   `json:"otpPending"`
	WebAuthnEnabled bool   `json:"webAuthnEnabled"`
}

// RequireMFARequest specifies which MFA type to require.
type RequireMFARequest struct {
	// Type must be "totp" or "webauthn".
	Type string `json:"type"`
}

// GetMFAStatus returns the MFA enrollment state for a user.
//
// Route: GET /api/v1/users/{id}/mfa-status
func (h *Handler) GetMFAStatus(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if err := ValidateUserID(userID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.requireUserInCallerOrg(w, r, userID) {
		return
	}

	creds, err := h.keycloak.GetUserCredentials(r.Context(), userID)
	if err != nil {
		h.logger.Error("failed to get user credentials", zap.String("user_id", userID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to retrieve MFA status")
		return
	}

	resp := MFAStatusResponse{UserID: userID}
	for _, c := range creds {
		switch c {
		case "otp":
			resp.OTPEnabled = true
		case "webauthn":
			resp.WebAuthnEnabled = true
		}
	}

	// Also check required actions (CONFIGURE_TOTP pending means OTP not yet enrolled).
	actions, actErr := h.keycloak.GetUserRequiredActions(r.Context(), userID)
	if actErr != nil {
		h.logger.Warn("failed to get user required actions", zap.String("user_id", userID), zap.Error(actErr))
	}
	for _, a := range actions {
		if a == "CONFIGURE_TOTP" {
			resp.OTPPending = true
		}
	}

	respondJSON(w, http.StatusOK, resp)
}

// RequireMFA sets a Keycloak required action so the user must enrol MFA on
// next login.
//
// Route: POST /api/v1/users/{id}/require-mfa
func (h *Handler) RequireMFA(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		respondError(w, http.StatusBadRequest, "id is required")
		return
	}
	if !h.requireUserInCallerOrg(w, r, userID) {
		return
	}

	var req RequireMFARequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var action string
	switch req.Type {
	case "totp":
		action = "CONFIGURE_TOTP"
	case "webauthn":
		action = "webauthn-register"
	default:
		respondError(w, http.StatusBadRequest, "type must be 'totp' or 'webauthn'")
		return
	}

	if err := h.keycloak.SetRequiredAction(r.Context(), userID, action); err != nil {
		h.logger.Error("failed to set required action",
			zap.String("user_id", userID), zap.String("action", action), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "failed to set MFA requirement")
		return
	}

	// Audit log — best-effort, detached context.
	if h.db != nil {
		actorID := middleware.GetActorID(r.Context())
		if auditErr := h.writeAuditEntryBestEffort(actorID, "require_mfa", "user", userID, map[string]interface{}{
			"mfa_type": req.Type, "action": action,
		}); auditErr != nil {
			h.logger.Warn("failed to write audit log for require_mfa", zap.Error(auditErr))
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"userId": userID, "action": action, "set": true,
	})
}
