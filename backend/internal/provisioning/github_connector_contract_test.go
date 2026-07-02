package provisioning

// A4 — GitHub Org connector contract tests.
//
// FreeCloud's "github" connector type is NOT GitHub's SCIM API — it manages
// GitHub Organization membership via GitHub's REST API
// (PUT/DELETE /orgs/{org}/memberships|members/{username}). This is a
// deliberate, already-documented design choice (see docs/DEPLOYMENT.md
// "Outbound Provisioning": "GitHub Org — Manages organization membership"),
// not a gap — GitHub's actual SCIM v2 Enterprise API is a separate,
// unimplemented surface. These tests pin the REAL GitHub REST API shapes
// (endpoint paths, request bodies, required headers) this connector already
// implements, using recorded-fixture responses matching GitHub's documented
// schemas (see docs.github.com/en/rest/orgs/members) so a future refactor
// can't silently drift from GitHub's actual contract.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// githubMembershipFixture mirrors GitHub's real "Get/Set organization
// membership for a user" response schema (role, state, organization, user,
// permissions) — see docs.github.com/en/rest/orgs/members. The connector only
// reads the HTTP status code today, but the fixture is realistic so this test
// would still catch a connector change that started depending on response
// fields.
const githubMembershipFixture = `{
	"url": "https://api.github.com/orgs/acme-corp/memberships/alice",
	"state": "active",
	"role": "member",
	"organization_url": "https://api.github.com/orgs/acme-corp",
	"organization": {"login": "acme-corp", "id": 12345},
	"user": {"login": "alice", "id": 67890}
}`

func TestGitHubConnector_ProvisionUser_MatchesRealAPIContract(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotAPIVersion, gotAccept string
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAPIVersion = r.Header.Get("X-GitHub-Api-Version")
		gotAccept = r.Header.Get("Accept")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(githubMembershipFixture))
	}))
	defer srv.Close()

	c := NewGitHubConnectorWithBaseURL("acme-corp", "gh-test-token", srv.URL)
	user := ProvisionableUser{Email: "alice@acme-corp.example", FirstName: "Alice", LastName: "Example"}

	remoteID, err := c.ProvisionUser(context.Background(), user)
	if err != nil {
		t.Fatalf("ProvisionUser: unexpected error: %v", err)
	}

	// --- Exact contract assertions ---
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT (GitHub's set-membership endpoint)", gotMethod)
	}
	if gotPath != "/orgs/acme-corp/memberships/alice" {
		t.Errorf("path = %q, want /orgs/acme-corp/memberships/alice", gotPath)
	}
	if gotAuth != "Bearer gh-test-token" {
		t.Errorf("Authorization header = %q, want Bearer gh-test-token", gotAuth)
	}
	// GitHub's REST API requires this header on every request since the
	// versioned-API rollout; omitting it can pin an unintended default version.
	if gotAPIVersion != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q, want 2022-11-28", gotAPIVersion)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept header = %q, want application/vnd.github+json", gotAccept)
	}
	if gotBody["role"] != "member" {
		t.Errorf("body[role] = %v, want \"member\" (GitHub's set-membership request shape)", gotBody["role"])
	}
	// The connector derives the GitHub username from the email's local part —
	// document that behavior via this assertion so a change to the derivation
	// logic shows up here, not just as a live-tenant surprise.
	if remoteID != "alice" {
		t.Errorf("remoteID = %q, want %q (derived from email local-part)", remoteID, "alice")
	}
}

func TestGitHubConnector_DeprovisionUser_MatchesRealAPIContract(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		// GitHub's "Remove organization membership for a user" returns 204
		// with no body — see docs.github.com/en/rest/orgs/members.
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewGitHubConnectorWithBaseURL("acme-corp", "gh-test-token", srv.URL)
	if err := c.DeprovisionUser(context.Background(), "alice"); err != nil {
		t.Fatalf("DeprovisionUser: unexpected error: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE (offboard-deactivation must remove org membership)", gotMethod)
	}
	if gotPath != "/orgs/acme-corp/members/alice" {
		t.Errorf("path = %q, want /orgs/acme-corp/members/alice", gotPath)
	}
}

// TestGitHubConnector_DeprovisionUser_AlreadyRemovedIsNotAnError proves the
// connector treats a 404 (user already removed / never a member) as success —
// GitHub's real API returns 404 in that case, and reconciliation retries
// must not treat "already gone" as a failure.
func TestGitHubConnector_DeprovisionUser_AlreadyRemovedIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewGitHubConnectorWithBaseURL("acme-corp", "gh-test-token", srv.URL)
	if err := c.DeprovisionUser(context.Background(), "bob"); err != nil {
		t.Errorf("DeprovisionUser with 404 (already removed): expected no error, got %v", err)
	}
}

// TestGitHubConnector_ProvisionUser_ErrorSurfacesBody proves a real GitHub
// error response (e.g. insufficient scope, rate limit) is surfaced in the
// returned error rather than swallowed.
func TestGitHubConnector_ProvisionUser_ErrorSurfacesBody(t *testing.T) {
	const ghErrorBody = `{"message":"Must have admin rights to Organization.","documentation_url":"https://docs.github.com/rest/orgs/members#set-organization-membership-for-a-user"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(ghErrorBody))
	}))
	defer srv.Close()

	c := NewGitHubConnectorWithBaseURL("acme-corp", "gh-test-token", srv.URL)
	_, err := c.ProvisionUser(context.Background(), ProvisionableUser{Email: "carol@example.com"})
	if err == nil {
		t.Fatal("ProvisionUser: expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "Must have admin rights") {
		t.Errorf("error %q does not surface GitHub's error message", err.Error())
	}
}
