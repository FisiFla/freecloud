// Failing-first tests for the SCIM connector hardening (security lane L2,
// issues 1, 2, 7): hardened HTTP client, truncated error bodies, bounded
// success decodes, SCIM filter escaping and client-side displayName check.
package provisioning

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOutboundSCIM_BlockedInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "tok")
	_, err := c.ProvisionUser(context.Background(), ProvisionableUser{Email: "a@b.com"})
	if err == nil {
		t.Fatal("expected the SCIM connector to refuse loopback in production")
	}
}

func TestOutboundSCIM_ErrorBodyTruncated(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	big := strings.Repeat("E", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(big))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "tok")
	_, err := c.ProvisionUser(context.Background(), ProvisionableUser{Email: "a@b.com"})
	if err == nil {
		t.Fatal("expected an error for the 400 response")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("error must say the body was truncated: %q", err.Error())
	}
	if len(err.Error()) > 900 {
		t.Fatalf("error still embeds too much body: %d bytes", len(err.Error()))
	}
}

func TestOutboundSCIM_SuccessDecodeBounded(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	huge := `{"id":"` + strings.Repeat("y", 11<<20) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(huge))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "tok")
	_, err := c.ProvisionUser(context.Background(), ProvisionableUser{Email: "a@b.com"})
	if err == nil {
		t.Fatal("expected an oversized success body to be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected a too-large error, got: %v", err)
	}
}

func TestOutboundSCIM_FilterEscapesQuoteAndBackslash(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	var capturedFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			capturedFilter = r.URL.Query().Get("filter")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"Resources":[]}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"group-1"}`))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "tok")
	name := `g" Schnecke\c`
	if _, err := c.findOrCreateGroup(context.Background(), name); err != nil {
		t.Fatalf("findOrCreateGroup: %v", err)
	}
	// SCIM filter strings use JSON string escaping (RFC 7644 §3.4.2.2).
	want := `displayName eq "g\" Schnecke\\c"`
	if capturedFilter != want {
		t.Fatalf("filter=%q want %q", capturedFilter, want)
	}
}

func TestOutboundSCIM_RejectsMismatchedDisplayName(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			// A hostile or confused server answers with a DIFFERENT group.
			w.Write([]byte(`{"Resources":[{"id":"wrong-id","displayName":"something-else"}]}`))
			return
		}
		created = true
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"created-id","displayName":"Team"}`))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "tok")
	id, err := c.findOrCreateGroup(context.Background(), "Team")
	if err != nil {
		t.Fatalf("findOrCreateGroup: %v", err)
	}
	if id == "wrong-id" {
		t.Fatal("the connector must not use a group whose displayName differs")
	}
	if !created {
		t.Fatal("the connector must create the group when the lookup mismatches")
	}
	if id != "created-id" {
		t.Fatalf("id=%q want created-id", id)
	}
}

func TestOutboundSCIM_ReusesMatchingDisplayName(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"Resources":[{"id":"g1","displayName":"Team"}]}`))
			return
		}
		created = true
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"g2"}`))
	}))
	defer srv.Close()

	c := NewSCIMConnector(srv.URL, "tok")
	id, err := c.findOrCreateGroup(context.Background(), "Team")
	if err != nil {
		t.Fatalf("findOrCreateGroup: %v", err)
	}
	if id != "g1" {
		t.Fatalf("id=%q want g1 (the matching group must be reused)", id)
	}
	if created {
		t.Fatal("no create must happen when the lookup matches")
	}
}
