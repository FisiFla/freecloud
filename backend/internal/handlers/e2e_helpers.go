package handlers

// E2E test-only helpers.
//
// These endpoints are ONLY registered when APP_ENV=test (see routes.go).
// They expose privileged operations (direct DB writes) that would be unsafe
// in any other environment. They are gated by the SCIM bearer token so they
// are not completely open, but they must never be reachable in production.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// E2ECreateEnrollmentTokenRequest is the body for the test-only token endpoint.
type E2ECreateEnrollmentTokenRequest struct {
	UserID string `json:"userId"`
}

// E2ECreateEnrollmentToken directly inserts an enrollment token for a user.
// This is intentionally only registered in APP_ENV=test; it bypasses the
// normal onboard→Fleet→callback flow so e2e tests can control enrollment
// without a live Keycloak JWT.
func (h *Handler) E2ECreateEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	var req E2ECreateEnrollmentTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if req.UserID == "" {
		respondError(w, http.StatusBadRequest, "userId is required")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()

	// Verify the user exists so we return a useful error instead of a FK violation.
	var foundUID string
	if err := h.db.QueryRow(ctx,
		`SELECT keycloak_user_id FROM users WHERE keycloak_user_id = $1`,
		req.UserID,
	).Scan(&foundUID); err != nil {
		respondError(w, http.StatusNotFound, "user not found")
		return
	}

	// Generate a deterministic-enough token for testing.
	token := "e2e-tok-" + req.UserID[:8] + "-" + strings.ReplaceAll(
		time.Now().UTC().Format("150405.000000000"), ".", "")

	if _, err := h.db.Exec(ctx,
		`INSERT INTO enrollment_tokens (token, user_id, expires_at)
		 VALUES ($1, $2, NOW() + INTERVAL '1 hour')`,
		token, req.UserID,
	); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create enrollment token")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"token": token})
}
