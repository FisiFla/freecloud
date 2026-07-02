//go:build e2e

// Package e2e — admin-JWT helper (A1).
//
// The e2e harness previously had no way to mint an authenticated admin JWT —
// SCIM and access-eval bearers are opaque tokens scoped to their own
// endpoints, so JWT-gated admin routes could only be smoke-tested (401-gated).
//
// The backend now supports an e2e-only bootstrap seam (E2E_SEED_ADMIN=true,
// fail-closed to APP_ENV=development/test — see backend/internal/bootstrap
// and config.IsDevOrE2E) that creates a known admin user and enables the
// OAuth2 Resource Owner Password Credentials grant on the public dashboard
// client. adminToken exchanges that admin's username/password for a real
// signed JWT directly from Keycloak's token endpoint, bypassing the
// browser/posture flow entirely (acceptable here: this is exercising the API
// layer directly, not the login flow, which has its own SPI-level e2e tests).
package e2e

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var (
	flagKeycloakURL   = flag.String("keycloak-url", envOr("E2E_KEYCLOAK_URL", "http://localhost:8083"), "Keycloak base URL")
	flagKeycloakRealm = flag.String("keycloak-realm", envOr("E2E_KEYCLOAK_REALM", "freecloud-e2e"), "Keycloak realm")
	flagAdminUsername = flag.String("admin-username", envOr("E2E_ADMIN_USERNAME", "e2e-admin"), "seeded e2e admin username")
	flagAdminPassword = flag.String("admin-password", envOr("E2E_ADMIN_PASSWORD", "e2e-admin-password"), "seeded e2e admin password")
	flagDashboardID   = flag.String("dashboard-client-id", envOr("E2E_DASHBOARD_CLIENT_ID", "freecloud-dashboard"), "public dashboard OIDC client id")
)

// adminToken exchanges the seeded e2e admin's credentials for a real signed
// JWT via Keycloak's token endpoint (direct/ROPC grant), retrying until
// Keycloak/bootstrap is ready or the timeout elapses. Fails the test (once,
// with the last error) if the deadline is reached — callers should treat the
// returned string as ready to use in an `Authorization: Bearer <token>` header.
func adminToken(t *testing.T, timeout time.Duration) string {
	t.Helper()

	tokenURL := strings.TrimRight(*flagKeycloakURL, "/") +
		"/realms/" + *flagKeycloakRealm + "/protocol/openid-connect/token"

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", *flagDashboardID)
	form.Set("username", *flagAdminUsername)
	form.Set("password", *flagAdminPassword)

	deadline := time.Now().Add(timeout)
	var lastErr string
	for time.Now().Before(deadline) {
		token, err := requestAdminToken(tokenURL, form)
		if err == nil {
			return token
		}
		lastErr = err.Error()
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("adminToken: Keycloak token endpoint not ready after %v: %s", timeout, lastErr)
	return ""
}

// requestAdminToken performs a single token-endpoint request.
func requestAdminToken(tokenURL string, form url.Values) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.AccessToken == "" {
		return "", fmt.Errorf("status %d: %s: %s", resp.StatusCode, body.Error, body.ErrorDescription)
	}
	return body.AccessToken, nil
}

// adminHeaders builds the Authorization header for an authenticated admin
// request, waiting up to 60s for Keycloak/bootstrap to be ready.
func adminHeaders(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"Authorization": "Bearer " + adminToken(t, 60*time.Second)}
}
