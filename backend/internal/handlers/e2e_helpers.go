package handlers

// E2E test-only helpers.
//
// These endpoints are ONLY registered when APP_ENV=test (see routes.go).
// They expose privileged operations (direct DB writes) that would be unsafe
// in any other environment. They are gated by the SCIM bearer token so they
// are not completely open, but they must never be reachable in production.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	// M3: enrollment_tokens stores only the sha256 hash (see
	// enrollment.go's enrollmentTokenHash) — this test-only seam must store
	// the SAME hash shape the real enrollment/device-identity lookups use,
	// or e2e tests minting a token here could never be consumed. org_id
	// isn't set explicitly here (falls back to the column's Default-Org
	// default) since this endpoint has no org context to draw from; it's
	// test-only tooling, not a production onboarding path.
	if _, err := h.db.Exec(ctx,
		`INSERT INTO enrollment_tokens (token_hash, user_id, expires_at)
		 VALUES ($1, $2, NOW() + INTERVAL '1 hour')`,
		enrollmentTokenHash(token), req.UserID,
	); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create enrollment token")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"token": token})
}

// E2ESeedOrgRequest is the body for the test-only org+token seeding endpoint.
type E2ESeedOrgRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// E2ESeedOrgResponse returns the created org plus a ready-to-use super-admin
// API token scoped to it.
type E2ESeedOrgResponse struct {
	OrgID string `json:"orgId"`
	Slug  string `json:"slug"`
	Token string `json:"token"`
}

// E2ESeedOrgWithAdminToken (C5 isolation e2e) creates a fresh organization
// and a super-admin-ROLE API token scoped to it, in one direct-DB-write call.
// "super-admin role" here means the token passes every RequirePermission
// gate (role is a global RBAC label) — but middleware.APITokenMiddleware
// resolves an API token's OrgContext from its OWN org_id column, not from
// org_memberships, so this token is still fully confined to the org it was
// minted for. Two tokens minted for two different orgs are exactly the
// "two org-admins" the isolation proof below needs.
//
// This mirrors E2ECreateEnrollmentToken's pattern precisely: intentionally
// only registered in APP_ENV=test, gated by the SCIM bearer token, bypasses
// the normal POST /api/v1/orgs + POST /api/v1/api-tokens flow (both of which
// require a super-admin JWT) because no admin-JWT e2e seam exists at this
// epic's branch point (a parallel epic — see the epic C brief — is adding an
// env-gated seeded-admin JWT path; once that lands, the isolation e2e suite
// should be reconciled to use real org-admin JWTs instead of this seam).
//
// Using an API token (rather than a JWT) as the e2e isolation credential is
// deliberate, not just a workaround: API tokens are already a first-class
// org-scoped auth mechanism (C2), so exercising isolation through them
// proves the exact code path a real MSP integration would use for
// service-to-service access to one org's data.
func (h *Handler) E2ESeedOrgWithAdminToken(w http.ResponseWriter, r *http.Request) {
	var req E2ESeedOrgRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Name == "" || req.Slug == "" {
		respondError(w, http.StatusBadRequest, "name and slug are required")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()

	var orgID string
	if err := h.db.QueryRow(ctx,
		`INSERT INTO organizations (name, slug) VALUES ($1, $2)
		 ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`,
		req.Name, req.Slug,
	).Scan(&orgID); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create organization")
		return
	}

	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	plaintext := "fc_" + hex.EncodeToString(rawBytes)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(plaintext)))

	if _, err := h.db.Exec(ctx,
		`INSERT INTO api_tokens (name, token_hash, role, scopes, service_identity, created_by, org_id)
		 VALUES ($1, $2, 'super-admin', $3, $4, 'e2e-seed', $5)`,
		"e2e-isolation-token", hash, []string{}, "e2e-isolation-"+req.Slug, orgID,
	); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create api token")
		return
	}

	respondJSON(w, http.StatusOK, E2ESeedOrgResponse{OrgID: orgID, Slug: req.Slug, Token: plaintext})
}
