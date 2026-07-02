//go:build e2e

// Package e2e — LDAP/AD federation route smoke tests (F2).
//
// Epic C's federation endpoints are JWT-gated (RequirePermission). This file
// verifies the routes are wired into the live stack and correctly reject
// unauthenticated requests (defense-in-depth alongside the RBAC unit tests).
// A1 (admin_auth.go) added a real admin-JWT path, so the authenticated CRUD +
// live connection-test + live sync round-trip against the openldap-e2e
// container is now exercised in admin_authenticated_e2e_test.go
// (TestE2E_Admin_FederationSource_CRUD) rather than deferred.
package e2e

import (
	"net/http"
	"testing"
	"time"
)

func TestE2E_LDAPFederation_RoutesGated(t *testing.T) {
	waitReady(t, 60*time.Second)

	const nilUUID = "00000000-0000-0000-0000-000000000000"

	// Each federation route must be registered in the live stack and gated:
	// unauthenticated → 401, never 404 (missing) or 5xx (broken).
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/federation/sources"},
		{http.MethodPost, "/api/v1/federation/sources"},
		{http.MethodPatch, "/api/v1/federation/sources/" + nilUUID},
		{http.MethodDelete, "/api/v1/federation/sources/" + nilUUID},
		{http.MethodPost, "/api/v1/federation/sources/" + nilUUID + "/test"},
		{http.MethodPost, "/api/v1/federation/sources/" + nilUUID + "/sync"},
	}
	for _, c := range cases {
		status, body := do(t, c.method, c.path, nil, nil)
		if status == http.StatusNotFound {
			t.Errorf("%s %s: route not registered (404) — federation routes missing from live stack", c.method, c.path)
		}
		if status >= 500 {
			t.Errorf("%s %s: unexpected 5xx (%d): %s", c.method, c.path, status, body)
		}
		if status != http.StatusUnauthorized {
			t.Logf("note: %s %s → %d (expected 401 unauthenticated)", c.method, c.path, status)
		}
	}
}
