// Failing-first tests for the Fleet client hardening (security lane L2,
// issues 1, 2): a hardened constructor for DB-configured URLs and truncated
// error bodies.
package fleet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewOutboundClient_BlockedInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"hosts":[]}`))
	}))
	defer srv.Close()

	c := NewOutboundClient(srv.URL, "tok")
	if _, err := c.GetHosts(context.Background(), ""); err == nil {
		t.Fatal("expected the outbound Fleet client to refuse loopback in production")
	}
}

func TestNewOutboundClient_AllowsInTest(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"hosts":[]}`))
	}))
	defer srv.Close()

	c := NewOutboundClient(srv.URL, "tok")
	hosts, err := c.GetHosts(context.Background(), "")
	if err != nil {
		t.Fatalf("test env must reach the Fleet mock: %v", err)
	}
	if len(hosts) != 0 {
		t.Fatalf("hosts=%v want empty", hosts)
	}
}

func TestOutboundClient_ErrorBodyTruncated(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	big := strings.Repeat("E", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(big))
	}))
	defer srv.Close()

	c := NewOutboundClient(srv.URL, "tok")
	_, err := c.GetHosts(context.Background(), "")
	if err == nil {
		t.Fatal("expected an error for the 500 response")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("error must say the body was truncated: %q", err.Error())
	}
	if len(err.Error()) > 900 {
		t.Fatalf("error still embeds too much body: %d bytes", len(err.Error()))
	}
}
