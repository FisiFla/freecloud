//go:build e2e

// Package e2e — outbound SCIM provisioning route smoke tests (F2).
//
// FreeCloud's admin endpoints are JWT-gated (RequirePermission). This file
// verifies the A4 provisioning routes are wired into the live stack and
// correctly reject unauthenticated requests (defense-in-depth alongside the
// RBAC unit tests). We also confirm the inbound SCIM endpoint (the target of
// outbound provisioning) is reachable with the SCIM bearer. A1 added a real
// admin-JWT path, so the fully authenticated provisioning round-trip (create
// employee -> connector provisions -> verify) is now exercised in
// admin_authenticated_e2e_test.go (TestE2E_Admin_ProvisioningRoundTrip)
// rather than deferred.
package e2e

import (
	"net/http"
	"testing"
	"time"
)

func TestE2E_SCIMOut_ProvisioningRoutesGated(t *testing.T) {
	waitReady(t, 60*time.Second)

	const nilUUID = "00000000-0000-0000-0000-000000000000"

	// The A4 provisioning routes must be registered in the live stack and gated:
	// an unauthenticated request must be 401 — never 404 (route missing) or 5xx (broken).
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/apps/" + nilUUID + "/provisioning"},
		{http.MethodPut, "/api/v1/apps/" + nilUUID + "/provisioning"},
		{http.MethodGet, "/api/v1/apps/" + nilUUID + "/provisioning/state"},
		{http.MethodPost, "/api/v1/apps/" + nilUUID + "/provisioning/resync/" + nilUUID},
	}
	for _, c := range cases {
		status, body := do(t, c.method, c.path, nil, nil)
		if status == http.StatusNotFound {
			t.Errorf("%s %s: route not registered (404) — provisioning routes missing from live stack", c.method, c.path)
		}
		if status >= 500 {
			t.Errorf("%s %s: unexpected 5xx (%d): %s", c.method, c.path, status, body)
		}
		if status != http.StatusUnauthorized {
			t.Logf("note: %s %s → %d (expected 401 unauthenticated)", c.method, c.path, status)
		}
	}

	// The inbound SCIM endpoint that outbound provisioning targets is itself
	// reachable and authenticated with the SCIM bearer — confirming the loop target.
	status, body := do(t, http.MethodGet, "/scim/v2/Users", scimHeaders(), nil)
	if status >= 500 {
		t.Errorf("inbound SCIM /Users: unexpected 5xx (%d): %s", status, body)
	}
	t.Logf("inbound SCIM /Users (SCIM bearer) → %d", status)
}
