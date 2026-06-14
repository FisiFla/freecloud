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
