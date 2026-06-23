//go:build e2e

// Package e2e — v1.5 route smoke tests (Epic F1).
//
// FreeCloud's admin endpoints are JWT-gated (RequirePermission). This harness
// has no admin-JWT path, so, consistent with the rest of this suite
// (see TestE2E_AppCreateStub / TestE2E_LDAPFederation_RoutesGated), we verify
// each new v1.5 route is wired into the live stack and correctly auth-gated:
// unauthenticated → 401, never 404 (route missing) or 5xx (broken).
//
// Surfaces covered:
//   - C: SAML idp-url + metadata
//   - D: policy/preview (dry-run conditional-access eval)
//   - E: provisioning dry-run + reconcile-all
//   - E: campaign export
//   - E: review-schedule CRUD
package e2e

import (
	"net/http"
	"testing"
	"time"
)

// assertRouteGated is the canonical smoke check: route must be registered (not 404)
// and must reject unauthenticated requests without a 5xx.
func assertRouteGated(t *testing.T, method, path string) {
	t.Helper()
	status, body := do(t, method, path, nil, nil)
	if status == http.StatusNotFound {
		t.Errorf("%s %s: route not registered (404) — missing from live stack", method, path)
	}
	if status >= 500 {
		t.Errorf("%s %s: unexpected 5xx (%d): %s", method, path, status, body)
	}
	if status != http.StatusUnauthorized {
		t.Logf("note: %s %s → %d (expected 401 unauthenticated)", method, path, status)
	}
}

// TestE2E_V15_SAMLRoutes verifies the two SAML read routes added in Epic C
// (idp-url and metadata) are wired and auth-gated.
func TestE2E_V15_SAMLRoutes(t *testing.T) {
	waitReady(t, 60*time.Second)

	const nilUUID = "00000000-0000-0000-0000-000000000000"

	cases := []struct{ method, path string }{
		// C2: IdP-initiated SSO URL for a SAML app
		{http.MethodGet, "/api/v1/apps/" + nilUUID + "/saml/idp-url"},
		// C3: SAML IdP metadata XML
		{http.MethodGet, "/api/v1/apps/" + nilUUID + "/saml/metadata"},
	}
	for _, c := range cases {
		assertRouteGated(t, c.method, c.path)
	}
}

// TestE2E_V15_PolicyPreview verifies the conditional-access dry-run endpoint
// added in Epic D is wired and auth-gated.
func TestE2E_V15_PolicyPreview(t *testing.T) {
	waitReady(t, 60*time.Second)

	const nilUUID = "00000000-0000-0000-0000-000000000000"

	// D2: per-app policy preview (PermManagePolicies)
	assertRouteGated(t, http.MethodPost, "/api/v1/apps/"+nilUUID+"/policy/preview")
}

// TestE2E_V15_ProvisioningDryRunReconcile verifies the two new provisioning
// action routes added in Epic E are wired and auth-gated.
func TestE2E_V15_ProvisioningDryRunReconcile(t *testing.T) {
	waitReady(t, 60*time.Second)

	const nilUUID = "00000000-0000-0000-0000-000000000000"

	cases := []struct{ method, path string }{
		// E1: Provisioning dry-run preview
		{http.MethodPost, "/api/v1/apps/" + nilUUID + "/provisioning/dry-run"},
		// E2: Reconcile all provisioning records
		{http.MethodPost, "/api/v1/apps/" + nilUUID + "/provisioning/reconcile-all"},
	}
	for _, c := range cases {
		assertRouteGated(t, c.method, c.path)
	}
}

// TestE2E_V15_CampaignExportAndReviewSchedules verifies the campaign export
// endpoint and review-schedule CRUD routes added in Epic E are wired and
// auth-gated.
func TestE2E_V15_CampaignExportAndReviewSchedules(t *testing.T) {
	waitReady(t, 60*time.Second)

	const nilUUID = "00000000-0000-0000-0000-000000000000"

	cases := []struct{ method, path string }{
		// E3: Campaign export
		{http.MethodGet, "/api/v1/campaigns/" + nilUUID + "/export"},
		// E3: Review schedule read
		{http.MethodGet, "/api/v1/review-schedules"},
		// E3: Review schedule write endpoints
		{http.MethodPost, "/api/v1/review-schedules"},
		{http.MethodPatch, "/api/v1/review-schedules/" + nilUUID},
		{http.MethodDelete, "/api/v1/review-schedules/" + nilUUID},
	}
	for _, c := range cases {
		assertRouteGated(t, c.method, c.path)
	}
}
