// Failing-first tests for the hardened outbound HTTP client (security lane
// L2, issue 1): admin-configured destinations must be re-validated at dial
// time, not just when they are saved.
package httpx

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsBlockedOutboundIP_Table(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", // loopback
		"169.254.169.254", "169.254.0.1", // link-local / cloud metadata
		"fe80::1",                       // IPv6 link-local
		"ff02::1",                       // IPv6 link-local multicast
		"10.0.0.1",                      // RFC1918
		"172.16.0.1",                    // RFC1918
		"192.168.1.1",                   // RFC1918
		"100.64.0.1", "100.127.255.255", // CGNAT
		"fc00::1", "fd00::1", // unique-local
		"0.0.0.0", "::", // unspecified
		"224.0.0.1", "239.255.255.250", // multicast
		"::ffff:10.0.0.1",        // IPv4-mapped RFC1918
		"::ffff:169.254.169.254", // IPv4-mapped metadata
		"::ffff:127.0.0.1",       // IPv4-mapped loopback
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if !IsBlockedOutboundIP(ip) {
			t.Errorf("IsBlockedOutboundIP(%s) = false, want true", s)
		}
	}

	allowed := []string{
		"8.8.8.8", "1.1.1.1", "203.0.113.10",
		"2001:4860:4860::8888",
	}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if IsBlockedOutboundIP(ip) {
			t.Errorf("IsBlockedOutboundIP(%s) = true, want false", s)
		}
	}
}

func TestValidateOutboundURL_Production(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	cases := []struct {
		url  string
		want bool
	}{
		{"https://8.8.8.8/v2", true},
		{"http://8.8.8.8/v2", false}, // http blocked outside dev/test
		{"https://169.254.169.254/latest", false},
		{"https://127.0.0.1/v2", false},
		{"https://10.0.0.5/v2", false},
		{"https://100.70.0.1/v2", false}, // CGNAT
		{"https://[::ffff:10.0.0.1]/v2", false},
		{"https://metadata.google.internal", false},
		{"not-a-url", false},
		{"", false},
	}
	for _, tc := range cases {
		err := ValidateOutboundURL("endpointUrl", tc.url)
		if (err == nil) != tc.want {
			t.Errorf("ValidateOutboundURL(%q) err=%v, want ok=%v", tc.url, err, tc.want)
		}
	}
}

func TestValidateOutboundURL_DevAllowsComposeMocks(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	for _, u := range []string{
		"http://localhost:9999/scim",
		"http://10.0.0.5:8080/scim",
		"http://127.0.0.1:8080/scim",
	} {
		if err := ValidateOutboundURL("endpointUrl", u); err != nil {
			t.Errorf("dev should allow %q: %v", u, err)
		}
	}
	// Link-local metadata stays blocked even in dev.
	if err := ValidateOutboundURL("endpointUrl", "http://169.254.169.254/"); err == nil {
		t.Error("dev must still block the link-local metadata address")
	}
}

func TestNewOutboundClient_BlocksLoopbackInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := NewOutboundClient(5 * time.Second)
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected the outbound client to refuse loopback in production")
	}
}

func TestNewOutboundClient_AllowsLoopbackInTest(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := NewOutboundClient(5 * time.Second)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("test env must reach httptest servers: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestNewOutboundClient_BlocksRedirectToPrivateIP(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	// Redirect target that must never be dialed once production blocking
	// is on: loopback is blocked in production, so the redirect check has
	// to reject it first. The fixture phase below allows the hit.
	var forbid atomic.Bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if forbid.Load() {
			t.Error("the redirect target must never be requested")
		}
		w.Write([]byte(`{"tried":true}`))
	}))
	defer evil.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
	}))
	defer front.Close()

	// Point the client at the front server through the test env first to
	// prove the fixture works, then enforce production blocking below.
	t.Setenv("APP_ENV", "test")
	plainOK := NewOutboundClient(5 * time.Second)
	resp, err := plainOK.Get(front.URL)
	if err != nil {
		t.Fatalf("fixture redirect failed in test env: %v", err)
	}
	resp.Body.Close()

	t.Setenv("APP_ENV", "production")
	forbid.Store(true)
	blocked := NewOutboundClient(5 * time.Second)
	if _, err := blocked.Get(front.URL); err == nil {
		t.Fatal("expected the redirect to a blocked address to be refused")
	}
}

func TestCheckOutboundRedirect_ForbidsDowngrade(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	prev, _ := http.NewRequest(http.MethodGet, "https://8.8.8.8/a", nil)
	t.Run("https to http", func(t *testing.T) {
		next, _ := http.NewRequest(http.MethodGet, "http://8.8.8.8/b", nil)
		if err := CheckOutboundRedirect(next, []*http.Request{prev}); err == nil {
			t.Fatal("expected https->http downgrade to be refused")
		}
	})
	t.Run("https to https", func(t *testing.T) {
		next, _ := http.NewRequest(http.MethodGet, "https://8.8.8.8/b", nil)
		if err := CheckOutboundRedirect(next, []*http.Request{prev}); err != nil {
			t.Fatalf("https->https public hop must pass: %v", err)
		}
	})
}

func TestCheckOutboundRedirect_CapsHops(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	next, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/x", nil)
	var via []*http.Request
	for i := 0; i < MaxOutboundRedirects+1; i++ {
		r, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/x", nil)
		via = append(via, r)
	}
	if err := CheckOutboundRedirect(next, via); err == nil {
		t.Fatal("expected the hop cap to be enforced")
	}
}

func TestOutboundDialControl_BlocksPrivate(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	for _, addr := range []string{"169.254.169.254:80", "127.0.0.1:80", "10.1.2.3:443", "[::ffff:10.0.0.1]:443"} {
		if err := OutboundDialControl("tcp", addr, nil); err == nil {
			t.Errorf("dial control must block %s in production", addr)
		}
	}
	if err := OutboundDialControl("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("dial control must allow public IPs: %v", err)
	}
	t.Setenv("APP_ENV", "test")
	if err := OutboundDialControl("tcp", "127.0.0.1:80", nil); err != nil {
		t.Errorf("dial control must allow loopback in test: %v", err)
	}
	if err := OutboundDialControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Error("dial control must block metadata even in test")
	}
}

func TestTruncateErrorBody(t *testing.T) {
	short := []byte("not found")
	if got := TruncateErrorBody(short); got != "not found" {
		t.Fatalf("short body changed: %q", got)
	}
	long := []byte(strings.Repeat("x", MaxErrorBodyBytes+100))
	got := TruncateErrorBody(long)
	if len(got) > MaxErrorBodyBytes+64 {
		t.Fatalf("truncated body still too long: %d bytes", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("truncated body must say so: %q", got)
	}
}

func TestDecodeBoundedJSON(t *testing.T) {
	var v struct {
		ID string `json:"id"`
	}
	if err := DecodeBoundedJSON(strings.NewReader(`{"id":"abc"}`), 0, &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.ID != "abc" {
		t.Fatalf("id=%q want abc", v.ID)
	}
	big := strings.NewReader(`{"id":"` + strings.Repeat("y", 2048) + `"}`)
	var v2 struct {
		ID string `json:"id"`
	}
	if err := DecodeBoundedJSON(big, 100, &v2); err == nil {
		t.Fatal("expected oversized JSON to be rejected")
	}
}
