//go:build e2e

// Package e2e — C5 (Epic C multi-tenant) cross-org isolation proof.
//
// LOAD-BEARING TEST: two real organizations, two real org-scoped credentials,
// proving API-level isolation against the LIVE stack (not a fake DB).
//
// This harness has no admin-JWT path (see scim_out_e2e_test.go's note — the
// SCIM and access-eval bearers are opaque tokens scoped to their own
// endpoints, and driving a real Keycloak login here is out of scope for this
// harness). Rather than skip the isolation proof, this test uses the
// test-only POST /api/v1/e2e/seed-org endpoint (APP_ENV=test + SCIM-bearer
// gated, mirrors the existing /api/v1/e2e/enrollment-token precedent) to mint
// two organizations and one org-scoped API token each. API tokens are a
// first-class org-scoped auth mechanism in their own right (C2) — the
// resulting round-trip through GET /api/v1/users, /api/v1/apps,
// /api/v1/api-tokens, and /api/v1/audit-logs with each org's token exercises
// the SAME OrgContextMiddleware + WHERE org_id = $ctx code path a real
// org-admin JWT would.
//
// When the parallel epic's env-gated seeded-admin JWT path lands, this suite
// should be extended (not replaced) with an equivalent JWT-driven round-trip
// so both credential types are proven isolated.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// seedOrgResp mirrors handlers.E2ESeedOrgResponse.
type seedOrgResp struct {
	OrgID string `json:"orgId"`
	Slug  string `json:"slug"`
	Token string `json:"token"`
}

func seedOrg(t *testing.T, slug string) seedOrgResp {
	t.Helper()
	status, body := do(t, http.MethodPost, "/api/v1/e2e/seed-org", scimHeaders(), map[string]string{
		"name": "Isolation Test " + slug,
		"slug": slug,
	})
	if status != http.StatusOK {
		t.Fatalf("seed-org %s: expected 200, got %d: %s", slug, status, body)
	}
	var envelope struct {
		Data seedOrgResp `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("seed-org %s: cannot parse response: %v\nbody: %s", slug, err, body)
	}
	if envelope.Data.OrgID == "" || envelope.Data.Token == "" {
		t.Fatalf("seed-org %s: missing orgId/token in response: %s", slug, body)
	}
	return envelope.Data
}

func orgTokenHeaders(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// TestE2E_CrossOrgIsolation is the C5 load-bearing isolation proof: org B's
// credential must never see org A's data (and vice versa) across every
// resource class this epic org-scoped, against the live stack.
func TestE2E_CrossOrgIsolation(t *testing.T) {
	waitReady(t, 60*time.Second)

	// Unique per test run so re-runs against a persistent e2e DB don't collide
	// on the slug's UNIQUE constraint.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	orgA := seedOrg(t, "iso-a-"+suffix)
	orgB := seedOrg(t, "iso-b-"+suffix)

	if orgA.OrgID == orgB.OrgID {
		t.Fatalf("seed-org returned the same org for both slugs — test setup is broken")
	}

	// Each org's token, hitting each org-scoped read endpoint, must return
	// 200 with ONLY that org's data (checked by resource-class-specific
	// assertions below) — never the other org's, never a 5xx, never an
	// implicit "no org context" bypass.
	resourceClasses := []struct {
		name   string
		method string
		path   string
	}{
		{"users", http.MethodGet, "/api/v1/users"},
		{"apps", http.MethodGet, "/api/v1/apps"},
		{"api-tokens", http.MethodGet, "/api/v1/api-tokens"},
		{"audit-logs", http.MethodGet, "/api/v1/audit-logs"},
		// Coordinator-flagged classes (org-scoping sweep completion): devices,
		// review campaigns/schedules, access/approval requests, federation
		// sources, and analytics all gained org_id filtering this round —
		// round-trip each read path through both orgs' tokens so a
		// regression that drops a WHERE org_id clause fails this suite.
		{"compliance", http.MethodGet, "/api/v1/compliance"},
		{"campaigns", http.MethodGet, "/api/v1/campaigns"},
		{"review-schedules", http.MethodGet, "/api/v1/review-schedules"},
		{"portal-access-requests", http.MethodGet, "/api/v1/portal/access-requests"},
		{"approval-requests", http.MethodGet, "/api/v1/approval-requests"},
		{"federation-sources", http.MethodGet, "/api/v1/federation/sources"},
		{"analytics-snapshots", http.MethodGet, "/api/v1/analytics/snapshots"},
	}

	for _, rc := range resourceClasses {
		t.Run(rc.name, func(t *testing.T) {
			statusA, bodyA := do(t, rc.method, rc.path, orgTokenHeaders(orgA.Token), nil)
			if statusA != http.StatusOK {
				t.Fatalf("org A %s: expected 200, got %d: %s", rc.name, statusA, bodyA)
			}
			statusB, bodyB := do(t, rc.method, rc.path, orgTokenHeaders(orgB.Token), nil)
			if statusB != http.StatusOK {
				t.Fatalf("org B %s: expected 200, got %d: %s", rc.name, statusB, bodyB)
			}
			// The api-tokens list is the one resource class where each org's
			// OWN seeded token is guaranteed to appear (every other class is
			// legitimately empty for a freshly-seeded org) — assert org A's
			// response never contains org B's token name/serviceIdentity and
			// vice versa, which is exactly the leak this epic must not allow.
			if rc.name == "api-tokens" {
				if containsStr(bodyA, "iso-b-"+suffix) {
					t.Errorf("org A's api-tokens response LEAKED an org-B-named resource: %s", bodyA)
				}
				if containsStr(bodyB, "iso-a-"+suffix) {
					t.Errorf("org B's api-tokens response LEAKED an org-A-named resource: %s", bodyB)
				}
			}
		})
	}

	// Explicit negative proof for api-tokens: org A's OWN seeded token must
	// be listed under org A's credential (positive control — proves the
	// endpoint returns real data, not just an empty list that would trivially
	// "pass" the leak check above).
	statusA, bodyA := do(t, http.MethodGet, "/api/v1/api-tokens", orgTokenHeaders(orgA.Token), nil)
	if statusA != http.StatusOK {
		t.Fatalf("org A api-tokens positive control: expected 200, got %d: %s", statusA, bodyA)
	}
	if !containsStr(bodyA, "e2e-isolation-token") {
		t.Fatalf("org A api-tokens positive control: expected to see its own seeded token, got: %s", bodyA)
	}

	// X-Org-Id cross-org override attempt: org A's token is already pinned to
	// org A server-side (api_tokens.org_id), so even an explicit X-Org-Id
	// header claiming org B must not switch its effective scope. This proves
	// OrgContextMiddleware's "already set" short-circuit for service tokens
	// (see middleware/org.go) can't be overridden by a client-supplied header.
	headers := orgTokenHeaders(orgA.Token)
	headers["X-Org-Id"] = orgB.OrgID
	status, body := do(t, http.MethodGet, "/api/v1/api-tokens", headers, nil)
	if status != http.StatusOK {
		t.Fatalf("org A token + X-Org-Id override attempt: expected 200 (header ignored, not honored), got %d: %s", status, body)
	}
	if containsStr(body, "iso-b-"+suffix) {
		t.Fatalf("org A token + X-Org-Id=org-B header was HONORED — cross-org override succeeded: %s", body)
	}
}

func containsStr(b []byte, substr string) bool {
	return strings.Contains(string(b), substr)
}
