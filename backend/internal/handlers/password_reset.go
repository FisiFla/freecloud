package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// ForgotPasswordRequest is the JSON body for POST /api/v1/auth/forgot-password.
type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

// passwordResetAsyncTimeout bounds the background reset work (local lookup +
// Keycloak execute-actions email) kicked off by ForgotPassword.
const passwordResetAsyncTimeout = 15 * time.Second

// ForgotPassword initiates a password reset flow via Keycloak's execute-actions
// email.  The response is deliberately ambiguous (no user enumeration).
//
// Anti-enumeration covers TIMING as well as content: the handler responds
// with the fixed generic body FIRST and performs the existence-dependent
// work (local user lookup + Keycloak send) in a bounded background
// goroutine afterwards. Calling Keycloak synchronously only when the email
// exists would make response time a user-existence oracle.
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

	// Snapshot dependencies for the background send. A detached timeout
	// context is used (never r.Context(), which is cancelled as soon as
	// this handler returns). Each send is time-bounded; request volume is
	// bounded upstream by the forgot-password rate limit.
	db := h.db
	kc := h.keycloak
	logger := h.logger
	email := req.Email

	// Always respond with the same body BEFORE doing any
	// existence-dependent work, so response time cannot reveal whether the
	// address exists.
	respondJSON(w, http.StatusOK, map[string]string{
		"message": "If an account with that email exists, a reset link has been sent.",
	})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), passwordResetAsyncTimeout)
		defer cancel()

		// Look up the Keycloak user ID from our local DB (if available) so
		// we can call the targeted execute-actions-email. ErrNoRows and any
		// DB error mean "send nothing" — the generic response above already
		// went out, so nothing is leaked either way.
		var kcUserID string
		if db != nil {
			_ = db.QueryRow(ctx,
				`SELECT keycloak_user_id FROM users WHERE email = $1`, email,
			).Scan(&kcUserID)
		}

		if kcUserID != "" {
			// Best-effort — log but don't surface errors to the caller.
			if err := kc.SendPasswordResetEmail(ctx, kcUserID); err != nil {
				logger.Sugar().Warnw("failed to send password reset email",
					"kc_user_id", kcUserID, "error", err)
			}
		}
	}()
}
