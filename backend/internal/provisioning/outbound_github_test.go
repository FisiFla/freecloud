// Failing-first tests for the GitHub connector hardening (security lane
// L2, issues 1, 2, 10): hardened HTTP client, truncated error bodies,
// escaped path segments and org-name validation with a legacy-URL path.
package provisioning

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOutboundGitHub_BlockedInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewGitHubConnectorWithBaseURL("acme-corp", "tok", srv.URL)
	_, err := c.ProvisionUser(context.Background(), ProvisionableUser{Email: "alice@example.com"})
	if err == nil {
		t.Fatal("expected the GitHub connector to refuse loopback in production")
	}
}

func TestOutboundGitHub_ErrorBodyTruncated(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	big := strings.Repeat("E", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(big))
	}))
	defer srv.Close()

	c := NewGitHubConnectorWithBaseURL("acme-corp", "tok", srv.URL)
	_, err := c.ProvisionUser(context.Background(), ProvisionableUser{Email: "alice@example.com"})
	if err == nil {
		t.Fatal("expected an error for the 403 response")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("error must say the body was truncated: %q", err.Error())
	}
	if len(err.Error()) > 900 {
		t.Fatalf("error still embeds too much body: %d bytes", len(err.Error()))
	}
}

func TestOutboundGitHub_PathSegmentsEscaped(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	var gotEscaped string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscaped = r.URL.EscapedPath()
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewGitHubConnectorWithBaseURL("acme-corp", "tok", srv.URL)
	// The username is derived from the email local part, which may contain
	// characters that are significant in a URL path.
	if _, err := c.ProvisionUser(context.Background(), ProvisionableUser{Email: "a/b@example.com"}); err != nil {
		t.Fatalf("ProvisionUser: %v", err)
	}
	if gotEscaped != "/orgs/acme-corp/memberships/a%2Fb" {
		t.Fatalf("escaped path=%q want /orgs/acme-corp/memberships/a%%2Fb", gotEscaped)
	}

	if err := c.DeprovisionUser(context.Background(), "x/y"); err != nil {
		t.Fatalf("DeprovisionUser: %v", err)
	}
	if gotEscaped != "/orgs/acme-corp/members/x%2Fy" {
		t.Fatalf("escaped path=%q want /orgs/acme-corp/members/x%%2Fy", gotEscaped)
	}
}

func TestNormalizeGitHubOrgRef(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"my-org", "my-org", false},
		{"  my-org  ", "my-org", false},
		{"a", "a", false},
		{"https://github.com/my-org", "my-org", false},
		{"https://github.com/my-org/repo", "my-org", false},
		{"https://api.github.com/orgs/my-org", "my-org", false},
		{"", "", true},
		{"not an org!!", "", true},
		{"-leading-dash", "", true},
		{strings.Repeat("a", 40), "", true}, // 39 chars max
		{"https://evil.com/my-org", "", true},
		{"http://github.com/my-org", "", true}, // https only
		{"https://github.com/", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeGitHubOrgRef(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeGitHubOrgRef(%q): want error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeGitHubOrgRef(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeGitHubOrgRef(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
