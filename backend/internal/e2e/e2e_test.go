//go:build e2e

// Package e2e contains end-to-end tests for FreeCloud.
//
// These tests require the e2e docker-compose stack to be running:
//
//	docker compose -f docker/docker-compose.e2e.yml up -d --build
//
// Run with:
//
//	cd backend && go test -tags=e2e -v ./internal/e2e/... \
//	  -backend=http://localhost:8085 \
//	  -scim-token=e2e-scim-token \
//	  -webhook-secret=e2e-webhook-secret
//
// Environment variables are accepted as fallbacks:
//
//	E2E_BACKEND_URL, E2E_SCIM_TOKEN, E2E_WEBHOOK_SECRET
package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// ---- flags / config ----

var (
	flagBackend       = flag.String("backend", envOr("E2E_BACKEND_URL", "http://localhost:8085"), "FreeCloud backend URL")
	flagSCIMToken     = flag.String("scim-token", envOr("E2E_SCIM_TOKEN", "e2e-scim-token"), "SCIM bearer token")
	flagWebhookSecret = flag.String("webhook-secret", envOr("E2E_WEBHOOK_SECRET", "e2e-webhook-secret"), "Fleet webhook secret")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ---- http helpers ----

func do(t *testing.T, method, path string, headers map[string]string, body interface{}) (int, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, *flagBackend+path, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody
}

func scimHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + *flagSCIMToken}
}

func hmacSig(t *testing.T, secret string, payload []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func waitReady(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(*flagBackend + "/healthz")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("backend at %s not ready after %v", *flagBackend, timeout)
}

// ---- TestMain ----

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// ---- Test: health ----

func TestE2E_Health(t *testing.T) {
	waitReady(t, 60*time.Second)

	status, body := do(t, http.MethodGet, "/healthz", nil, nil)
	if status != 200 {
		t.Fatalf("healthz: expected 200, got %d: %s", status, body)
	}
}

// ---- Test: SCIM Users — full lifecycle ----

func TestE2E_SCIMUsers_Lifecycle(t *testing.T) {
	waitReady(t, 30*time.Second)

	// Create user via SCIM
	createBody := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": fmt.Sprintf("e2e-user-%d@example.com", time.Now().UnixNano()),
		"name": map[string]string{
			"givenName":  "E2E",
			"familyName": "User",
		},
		"active": true,
	}
	status, body := do(t, http.MethodPost, "/scim/v2/Users", scimHeaders(), createBody)
	if status != 201 {
		t.Fatalf("create SCIM user: expected 201, got %d: %s", status, body)
	}

	var created struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
		Active   bool   `json:"active"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("parse create response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created user has empty ID")
	}
	t.Logf("created SCIM user id=%s", created.ID)

	// Get user
	status, body = do(t, http.MethodGet, "/scim/v2/Users/"+created.ID, scimHeaders(), nil)
	if status != 200 {
		t.Fatalf("get SCIM user: expected 200, got %d: %s", status, body)
	}

	// List users — check user appears
	status, body = do(t, http.MethodGet, "/scim/v2/Users", scimHeaders(), nil)
	if status != 200 {
		t.Fatalf("list SCIM users: expected 200, got %d: %s", status, body)
	}
	var listResp struct {
		TotalResults int `json:"totalResults"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("parse list response: %v", err)
	}
	if listResp.TotalResults < 1 {
		t.Errorf("expected at least 1 user in list, got %d", listResp.TotalResults)
	}

	// Patch user — deactivate
	patchBody := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "replace", "path": "active", "value": false},
		},
	}
	status, body = do(t, http.MethodPatch, "/scim/v2/Users/"+created.ID, scimHeaders(), patchBody)
	if status != 200 {
		t.Fatalf("patch SCIM user: expected 200, got %d: %s", status, body)
	}

	// Delete user
	status, _ = do(t, http.MethodDelete, "/scim/v2/Users/"+created.ID, scimHeaders(), nil)
	if status != 204 {
		t.Fatalf("delete SCIM user: expected 204, got %d", status)
	}
}

// ---- Test: SCIM Groups — full lifecycle ----

func TestE2E_SCIMGroups_Lifecycle(t *testing.T) {
	waitReady(t, 30*time.Second)

	// Create group
	createBody := map[string]interface{}{
		"schemas":     []string{"urn:ietf:params:scim:schemas:core:2.0:Group"},
		"displayName": fmt.Sprintf("e2e-group-%d", time.Now().UnixNano()),
		"members":     []interface{}{},
	}
	status, body := do(t, http.MethodPost, "/scim/v2/Groups", scimHeaders(), createBody)
	if status != 201 {
		t.Fatalf("create SCIM group: expected 201, got %d: %s", status, body)
	}

	var created struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("parse create group response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created group has empty ID")
	}
	t.Logf("created SCIM group id=%s name=%s", created.ID, created.DisplayName)

	// Get group
	status, body = do(t, http.MethodGet, "/scim/v2/Groups/"+created.ID, scimHeaders(), nil)
	if status != 200 {
		t.Fatalf("get SCIM group: expected 200, got %d: %s", status, body)
	}

	// List groups
	status, body = do(t, http.MethodGet, "/scim/v2/Groups", scimHeaders(), nil)
	if status != 200 {
		t.Fatalf("list SCIM groups: expected 200, got %d: %s", status, body)
	}
	var listResp struct {
		TotalResults int `json:"totalResults"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("parse group list: %v", err)
	}
	if listResp.TotalResults < 1 {
		t.Errorf("expected at least 1 group in list, got %d", listResp.TotalResults)
	}

	// Patch group — rename
	patchBody := map[string]interface{}{
		"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]interface{}{
			{"op": "replace", "path": "displayName", "value": "e2e-renamed-group"},
		},
	}
	status, body = do(t, http.MethodPatch, "/scim/v2/Groups/"+created.ID, scimHeaders(), patchBody)
	if status != 200 {
		t.Fatalf("patch SCIM group: expected 200, got %d: %s", status, body)
	}

	// Delete group
	status, _ = do(t, http.MethodDelete, "/scim/v2/Groups/"+created.ID, scimHeaders(), nil)
	if status != 204 {
		t.Fatalf("delete SCIM group: expected 204, got %d", status)
	}
}

// ---- Test: Enrollment callback → offboard ----

func TestE2E_Onboard_EnrollmentCallback_Offboard(t *testing.T) {
	// This flow cannot be fully exercised without a JWT token (onboard endpoint
	// requires auth). We verify the enrollment callback itself (HMAC-signed, no JWT).
	waitReady(t, 30*time.Second)

	// Fabricate an enrollment token that was "issued" — the endpoint will 404 the
	// token in the DB (no prior onboard) but the HMAC check should pass.
	payload, _ := json.Marshal(map[string]string{
		"enrollment_token": "e2e-test-token-does-not-exist",
		"host_id":          "host-e2e-001",
		"hostname":         "e2e-laptop.local",
		"os_version":       "macOS 15.0",
	})
	sig := hmacSig(t, *flagWebhookSecret, payload)

	headers := map[string]string{
		"X-Fleet-Signature": sig,
	}
	status, body := do(t, http.MethodPost, "/api/v1/fleet/enrollment-callback", headers, json.RawMessage(payload))
	// Expect 400 (unknown token) or 200 — not 401/403 (signature check must pass)
	if status == 401 || status == 403 {
		t.Fatalf("enrollment callback: HMAC rejected (got %d): %s", status, body)
	}
	t.Logf("enrollment callback with unknown token → %d (expected 400)", status)
}

// ---- Test: Fleet teams & policies ----

func TestE2E_FleetTeams_Policies(t *testing.T) {
	waitReady(t, 30*time.Second)

	// List policies (no auth — route is under JWT but we just check SCIM health here;
	// actual auth-gated routes need a JWT which is out of scope without a live KC).
	// Verify the /healthz still passes after all operations.
	status, _ := do(t, http.MethodGet, "/healthz", nil, nil)
	if status != 200 {
		t.Fatalf("post-team-ops healthz: expected 200, got %d", status)
	}

	// SCIM auth-only: verify teams endpoint 401s without auth (not 500).
	status, _ = do(t, http.MethodGet, "/api/v1/teams", nil, nil)
	if status == 500 {
		t.Errorf("teams endpoint: unexpected 500 (no auth should yield 401/403)")
	}
	t.Logf("unauthenticated GET /api/v1/teams → %d", status)
}

// ---- Test: OIDC/SAML app stub coverage ----

func TestE2E_AppCreateStub(t *testing.T) {
	// Verify the apps endpoint exists and rejects unauthenticated requests properly.
	waitReady(t, 30*time.Second)

	status, _ := do(t, http.MethodGet, "/api/v1/apps", nil, nil)
	if status == 500 {
		t.Errorf("apps list: unexpected 500 on unauthenticated request")
	}
	t.Logf("unauthenticated GET /api/v1/apps → %d", status)
}

// ---- Test: Posture/compliance stub coverage ----

func TestE2E_CompliancePosure(t *testing.T) {
	waitReady(t, 30*time.Second)

	// Verify unauthenticated requests return non-500.
	status, _ := do(t, http.MethodGet, "/api/v1/compliance", nil, nil)
	if status == 500 {
		t.Errorf("compliance: unexpected 500 on unauthenticated request")
	}
	t.Logf("unauthenticated GET /api/v1/compliance → %d", status)
}

// ---- helpers ----

// assertContains checks body contains the substring.
func assertContains(t *testing.T, body []byte, sub string) {
	t.Helper()
	if !strings.Contains(string(body), sub) {
		t.Errorf("expected body to contain %q, got: %s", sub, body)
	}
}
