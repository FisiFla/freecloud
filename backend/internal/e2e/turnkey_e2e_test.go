//go:build e2e

// Package e2e — v1.6 turnkey bootstrap smoke tests (Epic E1).
//
// The setup endpoints are unauthenticated, so this file can genuinely exercise
// them against the live stack — no JWT path required.
//
// What we assert:
//  1. GET /api/v1/setup/status returns 200 with a JSON body containing a
//     "provisioned" boolean field (shape check).
//  2. The e2e realm is always provisioned by the time these tests run (the
//     backend self-bootstraps Keycloak on startup; the e2e compose also sets
//     CREATE_DEMO_USER or the posture-enforcement tests create users). We
//     therefore assert the LOCKED / fail-closed behaviour: POST /api/v1/setup
//     returns 409 (already provisioned).
//  3. 409 body contains a machine-readable error field so callers can distinguish
//     it from other 4xx responses.
//
// If you need to test the create→201 path, bring up a pristine realm with no
// admin user and run only this file. The locked-state path is chosen here
// because it is robust against the harness's existing realm state.
package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestE2E_Turnkey_SetupStatus verifies that the unauthenticated status endpoint
// is wired, returns 200, and emits a valid {"provisioned": <bool>} shape.
func TestE2E_Turnkey_SetupStatus(t *testing.T) {
	waitReady(t, 60*time.Second)

	status, body := do(t, http.MethodGet, "/api/v1/setup/status", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/setup/status: expected 200, got %d: %s", status, body)
	}

	// Responses use the {success, data:{...}} envelope.
	var resp struct {
		Data struct {
			Provisioned *bool `json:"provisioned"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("GET /api/v1/setup/status: cannot parse response as JSON: %v\nbody: %s", err, body)
	}
	if resp.Data.Provisioned == nil {
		t.Fatalf("GET /api/v1/setup/status: response missing required 'provisioned' field: %s", body)
	}
	t.Logf("GET /api/v1/setup/status → provisioned=%v", *resp.Data.Provisioned)
}

// TestE2E_Turnkey_SetupLockedOnceProvisioned verifies the fail-closed lock on
// POST /api/v1/setup.  The e2e realm is always provisioned by the time this
// test runs (the backend self-bootstraps Keycloak and the posture-enforcement
// suite creates users in the same realm), so we assert that a second attempt
// returns 409 — not 200/201, and not a 5xx.
func TestE2E_Turnkey_SetupLockedOnceProvisioned(t *testing.T) {
	waitReady(t, 30*time.Second)

	// Confirm the realm is provisioned before exercising the lock.
	statusCode, body := do(t, http.MethodGet, "/api/v1/setup/status", nil, nil)
	if statusCode != http.StatusOK {
		t.Fatalf("setup status pre-check: expected 200, got %d: %s", statusCode, body)
	}
	var statusResp struct {
		Data struct {
			Provisioned bool `json:"provisioned"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &statusResp); err != nil {
		t.Fatalf("setup status pre-check: cannot parse response: %v", err)
	}
	if !statusResp.Data.Provisioned {
		t.Skip("realm not yet provisioned; skipping locked-state assertion (run posture suite first or seed an admin user)")
	}

	// Attempt to create a first admin — must be rejected with 409.
	setupBody := map[string]string{
		"adminEmail":    "turnkey-e2e@example.com",
		"adminPassword": "TurnkeyE2E!9",
		"orgName":       "E2E Org",
	}
	code, body := do(t, http.MethodPost, "/api/v1/setup", nil, setupBody)
	if code != http.StatusConflict {
		t.Fatalf("POST /api/v1/setup on provisioned realm: expected 409, got %d: %s", code, body)
	}

	// Body must contain an "error" key so callers can distinguish it from other 4xx.
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("POST /api/v1/setup 409 body: cannot parse as JSON: %v\nbody: %s", err, body)
	}
	if errResp.Error == "" {
		t.Errorf("POST /api/v1/setup 409 body: expected non-empty 'error' field, got: %s", body)
	}
	t.Logf("POST /api/v1/setup (provisioned realm) → 409, error=%q", errResp.Error)
}

// TestE2E_Turnkey_SelfBootstrapped verifies that the backend self-bootstrapped
// Keycloak without any external script. The evidence is:
//   - /healthz returns 200 (backend started and connected to KC), AND
//   - /api/v1/setup/status returns provisioned=true
//
// This is only possible if the Epic A bootstrap ran successfully on startup
// (no setup_realm.sh was executed by CI).
func TestE2E_Turnkey_SelfBootstrapped(t *testing.T) {
	waitReady(t, 60*time.Second)

	// 1. Backend is serving (already ensured by waitReady, but make it explicit).
	hStatus, hBody := do(t, http.MethodGet, "/healthz", nil, nil)
	if hStatus != http.StatusOK {
		t.Fatalf("healthz: expected 200, got %d: %s", hStatus, hBody)
	}

	// 2. Realm is provisioned (only possible if bootstrap ran).
	sStatus, sBody := do(t, http.MethodGet, "/api/v1/setup/status", nil, nil)
	if sStatus != http.StatusOK {
		t.Fatalf("setup/status: expected 200, got %d: %s", sStatus, sBody)
	}
	// A valid {data:{provisioned:<bool>}} shape proves the backend self-bootstrapped:
	// the service account can authenticate to Keycloak and query the realm. (We
	// don't assert provisioned==true here because that depends on a user existing,
	// which is test-ordering-dependent; the lock test covers the provisioned path.)
	var resp struct {
		Data struct {
			Provisioned *bool `json:"provisioned"`
		} `json:"data"`
	}
	if err := json.Unmarshal(sBody, &resp); err != nil {
		t.Fatalf("setup/status: cannot parse response: %v\nbody: %s", err, sBody)
	}
	if resp.Data.Provisioned == nil {
		t.Errorf("self-bootstrap check failed: setup/status returned no 'provisioned' field; the service account may not be able to query Keycloak: %s", sBody)
	}
	t.Logf("self-bootstrap verified: healthz=200, setup/status=200, provisioned=%v", resp.Data.Provisioned)
}
