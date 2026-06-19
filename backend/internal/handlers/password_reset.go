package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ForgotPasswordRequest is the JSON body for POST /api/v1/auth/forgot-password.
type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

// ForgotPassword initiates a password reset flow via Keycloak's execute-actions
// email.  The response is deliberately ambiguous (no user enumeration).
//
// Route: POST /api/v1/auth/forgot-password  — public, no JWT required.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req ForgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Return the same generic message to avoid leaking parse errors.
		respondJSON(w, http.StatusOK, map[string]string{
			"message": "If an account with that email exists, a reset link has been sent.",
		})
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// Fail-fast on obviously invalid input — but still return the generic message
	// so callers can't distinguish "bad email format" from "no such user".
	if req.Email == "" || !isValidEmail(req.Email) {
		respondJSON(w, http.StatusOK, map[string]string{
			"message": "If an account with that email exists, a reset link has been sent.",
		})
		return
	}

	// Look up the Keycloak user ID from our local DB (if available) so we can
	// call the targeted execute-actions-email.  We do NOT return an error when
	// the user isn't found — that would leak user existence.
	var kcUserID string
	if h.db != nil {
		_ = h.db.QueryRow(r.Context(),
			`SELECT keycloak_user_id FROM users WHERE email = $1`, req.Email,
		).Scan(&kcUserID)
		// Deliberate: ignore ErrNoRows and any DB error — fall through to the
		// generic response.
	}

	if kcUserID != "" {
		// Best-effort — log but don't surface errors to the caller.
		if err := h.keycloak.SendPasswordResetEmail(r.Context(), kcUserID); err != nil {
			h.logger.Sugar().Warnw("failed to send password reset email",
				"kc_user_id", kcUserID, "error", err)
		}
	}

	// Always return the same response to prevent user enumeration.
	respondJSON(w, http.StatusOK, map[string]string{
		"message": "If an account with that email exists, a reset link has been sent.",
	})
}
