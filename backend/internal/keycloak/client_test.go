package keycloak

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSAMLAttributesBuilt verifies that buildSAMLAttributes sets all overridable fields
// when opts are fully specified.
func TestSAMLAttributesBuilt(t *testing.T) {
	opts := &SAMLOptions{
		SigningAlgorithm:  "RSA_SHA512",
		EncryptAssertions: true,
		NameIDFormat:      "email",
		AttributeMappings: []SAMLAttributeMapping{
			{UserAttribute: "department", SAMLAttributeName: "dept"},
		},
	}
	attrs := buildSAMLAttributes("my-app", "https://sp.example.com/acs", "https://sp.example.com", opts)

	if got := attrs["saml.signature.algorithm"]; got != "RSA_SHA512" {
		t.Errorf("signing algorithm: got %q, want RSA_SHA512", got)
	}
	if got := attrs["saml.encrypt"]; got != "true" {
		t.Errorf("encrypt: got %q, want true", got)
	}
	if got := attrs["saml_name_id_format"]; got != "email" {
		t.Errorf("nameIDFormat: got %q, want email", got)
	}
	if got := attrs["saml.idp.initiated.sso.url.name"]; got != "my-app" {
		t.Errorf("idp initiated sso url name: got %q, want my-app", got)
	}
	if got := attrs["saml_sp_entity_id"]; got != "https://sp.example.com" {
		t.Errorf("entity id: got %q", got)
	}
	if got := attrs["saml.assertion.consumer.service.post.binding.url"]; got != "https://sp.example.com/acs" {
		t.Errorf("acs url: got %q", got)
	}
}

// TestSAMLAttributesDefaults verifies that nil opts produces safe interoperable defaults.
func TestSAMLAttributesDefaults(t *testing.T) {
	attrs := buildSAMLAttributes("app", "https://sp.example.com/acs", "https://sp.example.com", nil)

	if got := attrs["saml.signature.algorithm"]; got != "RSA_SHA256" {
		t.Errorf("default signing algorithm: got %q, want RSA_SHA256", got)
	}
	if got := attrs["saml.encrypt"]; got != "false" {
		t.Errorf("default encrypt: got %q, want false", got)
	}
	if got := attrs["saml_name_id_format"]; got != "persistent" {
		t.Errorf("default nameIDFormat: got %q, want persistent", got)
	}
}

// TestSAMLAttributesUnknownValuesReset verifies that invalid option values
// fall back to safe defaults rather than passing invalid strings to Keycloak.
func TestSAMLAttributesUnknownValuesReset(t *testing.T) {
	opts := &SAMLOptions{
		SigningAlgorithm: "BOGUS_ALGO",
		NameIDFormat:     "invalid-format",
	}
	attrs := buildSAMLAttributes("app", "", "", opts)

	if got := attrs["saml.signature.algorithm"]; got != "RSA_SHA256" {
		t.Errorf("unknown algo should fall back to RSA_SHA256, got %q", got)
	}
	if got := attrs["saml_name_id_format"]; got != "persistent" {
		t.Errorf("unknown nameIDFormat should fall back to persistent, got %q", got)
	}
}

// TestSAMLNameSanitization verifies samlSlug produces valid URL-safe slugs.
func TestSAMLNameSanitization(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"my-app", "my-app"},
		{"My App", "my-app"},
		{"My  App!!!", "my-app"},
		{"  leading trailing  ", "leading-trailing"},
		{"", "app"},
		{"---", "app"},
		{"Hello World 123", "hello-world-123"},
		{"café", "caf"}, // accented char becomes '-' then trimmed by Trim
	}
	for _, tc := range cases {
		got := samlSlug(tc.input)
		if got != tc.want {
			t.Errorf("samlSlug(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestIsConflictErr confirms the helper correctly distinguishes a 409 Conflict
// (which AssignUserToClient treats as "role already exists") from real errors.
func TestIsConflictErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"409 conflict in message", errors.New("CreateClientRole: 409 Conflict, role already exists"), true},
		{"conflict lowercase", errors.New("create role: conflict detected"), true},
		{"404 not found", errors.New("GetClientRole: 404 Not Found"), false},
		{"500 server error", errors.New("unexpected 500"), false},
		{"network error", errors.New("dial tcp: connection refused"), false},
		{"empty message", errors.New(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isConflictErr(tt.err); got != tt.want {
				t.Errorf("isConflictErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestLoginCachesToken proves the admin token is fetched once and reused until
// it nears expiry, instead of a fresh client-credentials login per operation.
func TestLoginCachesToken(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok-123","expires_in":300,"token_type":"Bearer"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	k := NewClient(srv.URL, "client", "secret", "myrealm")

	tok, err := k.login(context.Background())
	if err != nil {
		t.Fatalf("login 1: %v", err)
	}
	if tok != "tok-123" {
		t.Fatalf("unexpected token %q", tok)
	}
	if _, err := k.login(context.Background()); err != nil {
		t.Fatalf("login 2: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected 1 token fetch (second should be cached), got %d", got)
	}

	// Force the cached token to be near-expiry; the next login must refetch.
	k.mu.Lock()
	k.tokenExpiry = time.Now().Add(-time.Minute)
	k.mu.Unlock()
	if _, err := k.login(context.Background()); err != nil {
		t.Fatalf("login 3: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("expected 2 token fetches after expiry, got %d", got)
	}
}
