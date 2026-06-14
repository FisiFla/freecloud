package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient points a FleetClient at the given test server with a dummy token.
func newTestClient(t *testing.T, srv *httptest.Server) *FleetClient {
	t.Helper()
	return NewClient(srv.URL, "test-token")
}

// TestGetHosts_ParsesHosts confirms the happy path parses the hosts slice.
func TestGetHosts_ParsesHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/fleet/hosts" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing/wrong auth header: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hosts": []map[string]any{
			{"id": "h1", "hostname": "mac-1", "os_version": "macOS 15", "status": "online"},
		}})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	hosts, err := c.GetHosts(context.Background(), "")
	if err != nil {
		t.Fatalf("GetHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0].ID != "h1" {
		t.Fatalf("unexpected hosts: %+v", hosts)
	}
}

// TestGetHosts_EncodesQuery confirms the query is URL-escaped.
func TestGetHosts_EncodesQuery(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"hosts": []any{}})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.GetHosts(context.Background(), "foo bar&baz"); err != nil {
		t.Fatalf("GetHosts: %v", err)
	}
	// The raw ampersand and space must be percent-encoded, not injected raw.
	if strings.Contains(capturedQuery, " foo") || strings.Contains(capturedQuery, "&baz") && !strings.Contains(capturedQuery, "%26baz") {
		t.Fatalf("query not properly escaped: %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "query=foo") {
		t.Fatalf("expected query= prefix, got %q", capturedQuery)
	}
}

// TestNon2xxReturnsError confirms a non-2xx response surfaces an error.
func TestNon2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"errors": "fleet down"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetHosts(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for 503, got nil")
	}
}

// TestInvalidJSONReturnsParseError confirms malformed JSON is reported.
func TestInvalidJSONReturnsParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetHosts(context.Background(), "")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

// TestGetHostSecurityState_SoftwareFailureSetsUnknownVulns confirms that when
// the software sub-fetch fails, UnknownVulns is true but the call still succeeds.
func TestGetHostSecurityState_SoftwareFailureSetsUnknownVulns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The host detail endpoint must succeed...
		if r.URL.Path == "/api/v1/fleet/hosts/host-1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"host": map[string]any{"disk_encryption": true, "firewall": true},
			})
			return
		}
		// ...but the software endpoint returns an error.
		if r.URL.Path == "/api/v1/fleet/hosts/host-1/software" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	state, err := c.GetHostSecurityState(context.Background(), "host-1")
	if err != nil {
		t.Fatalf("expected success with partial data, got %v", err)
	}
	if !state.UnknownVulns {
		t.Error("expected UnknownVulns=true when software fetch fails")
	}
	if !state.FirewallEnabled || !state.DiskEncrypted {
		t.Error("expected posture fields to still be populated from the host detail")
	}
}

// TestGetHostSecurityState_HostFailureFailsClosed confirms that if the host
// detail fetch itself fails, the whole call returns an error (fail-closed).
func TestGetHostSecurityState_HostFailureFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetHostSecurityState(context.Background(), "host-1")
	if err == nil {
		t.Fatal("expected error when host detail endpoint fails (fail-closed)")
	}
}

// TestValidateHostID confirms the hostID guard blocks traversal / injection.
func TestValidateHostID(t *testing.T) {
	tests := []struct {
		hostID  string
		wantErr bool
	}{
		{"host-1", false},
		{"abc123", false},
		{"550e8400-e29b-41d4-a716-446655440000", false},
		{"", true},
		{"../admin", true},
		{"host?x=1", true},
		{"host#frag", true},
		{"host;rm -rf", true},
		{"host name", true}, // space
	}
	for _, tt := range tests {
		t.Run(tt.hostID, func(t *testing.T) {
			err := validateHostID(tt.hostID)
			if tt.wantErr && err == nil {
				t.Errorf("validateHostID(%q): expected error, got nil", tt.hostID)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateHostID(%q): expected nil, got %v", tt.hostID, err)
			}
		})
	}
}

// TestGetHostSoftware_RejectsBadHostID confirms the guard runs before any HTTP
// call, so a malicious hostID cannot reach the wire.
func TestGetHostSoftware_RejectsBadHostID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request should not reach the server for an invalid hostID: %s", r.URL.Path)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.GetHostSoftware(context.Background(), "../admin/users")
	if err == nil {
		t.Fatal("expected error for path-traversal hostID")
	}
}

// TestIssueRemoteWipe_HappyPath confirms a 200 returns nil.
func TestIssueRemoteWipe_HappyPath(t *testing.T) {
	var path, method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		method = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.IssueRemoteWipe(context.Background(), "host-1"); err != nil {
		t.Fatalf("IssueRemoteWipe: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("expected POST, got %s", method)
	}
	if path != "/api/v1/fleet/hosts/host-1/wipe" {
		t.Errorf("unexpected path: %s", path)
	}
}

// TestPing_Health confirms Ping hits the status endpoint.
func TestPing_Health(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if path != "/api/v1/fleet/status" {
		t.Errorf("expected /api/v1/fleet/status, got %s", path)
	}
}
