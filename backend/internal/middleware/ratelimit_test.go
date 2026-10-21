package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// okHandler is a trivial 200 handler used by the rate-limiter tests.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func doRequest(t *testing.T, h http.Handler, remoteAddr, xff string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", nil)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// newTestLimiter builds an in-memory limiter with no trusted proxies.
func newTestLimiter(t *testing.T, limit int, window time.Duration) *RateLimiter {
	t.Helper()
	rl, err := NewRateLimiterWithProxies(limit, window, nil)
	if err != nil {
		t.Fatalf("NewRateLimiterWithProxies: %v", err)
	}
	t.Cleanup(rl.Stop)
	return rl
}

// TestRateLimiterSameHostDifferentPorts collapses to one bucket.
func TestRateLimiterSameHostDifferentPorts(t *testing.T) {
	rl := newTestLimiter(t, 2, time.Minute)
	h := rl.Middleware(okHandler)

	// Two requests from the same host but different ephemeral ports must both
	// be allowed (counted against the same limit), and a third must be blocked.
	if c := doRequest(t, h, "127.0.0.1:51000", ""); c != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", c)
	}
	if c := doRequest(t, h, "127.0.0.1:51001", ""); c != http.StatusOK {
		t.Fatalf("second request (new port): expected 200, got %d", c)
	}
	if c := doRequest(t, h, "127.0.0.1:51002", ""); c != http.StatusTooManyRequests {
		t.Fatalf("third request (over limit): expected 429, got %d", c)
	}
}

// TestRateLimiterReturns429 over the configured limit.
func TestRateLimiterReturns429(t *testing.T) {
	rl := newTestLimiter(t, 1, time.Minute)
	h := rl.Middleware(okHandler)

	if c := doRequest(t, h, "10.0.0.1:4000", ""); c != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", c)
	}
	if c := doRequest(t, h, "10.0.0.1:4001", ""); c != http.StatusTooManyRequests {
		t.Fatalf("second: expected 429, got %d", c)
	}
}

// TestRateLimiterWindowExpiry allows requests again after the window elapses.
func TestRateLimiterWindowExpiry(t *testing.T) {
	rl := newTestLimiter(t, 1, 30*time.Millisecond)
	h := rl.Middleware(okHandler)

	if c := doRequest(t, h, "192.168.1.1:6000", ""); c != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", c)
	}
	if c := doRequest(t, h, "192.168.1.1:6001", ""); c != http.StatusTooManyRequests {
		t.Fatalf("second (within window): expected 429, got %d", c)
	}
	// Wait for the window (plus GC tick) to expire.
	time.Sleep(90 * time.Millisecond)
	if c := doRequest(t, h, "192.168.1.1:6002", ""); c != http.StatusOK {
		t.Fatalf("after window expiry: expected 200, got %d", c)
	}
}

// TestRateLimiterIgnoresForwardedHeader confirms that a spoofed
// X-Forwarded-For does not change the rate-limit key when no proxy is
// trusted.
func TestRateLimiterIgnoresForwardedHeader(t *testing.T) {
	rl := newTestLimiter(t, 1, time.Minute)
	h := rl.Middleware(okHandler)

	// First request from 172.16.0.1 with one spoofed XFF value.
	if c := doRequest(t, h, "172.16.0.1:7000", "9.9.9.9"); c != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", c)
	}
	// Second request from the same host but a DIFFERENT spoofed XFF value.
	// If XFF were trusted, this would be a fresh bucket and return 200.
	// Because we key on RemoteAddr host only, it must be 429.
	if c := doRequest(t, h, "172.16.0.1:7001", "8.8.8.8"); c != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF should not bypass limit: expected 429, got %d", c)
	}
}

// TestRateLimiterDistinctHostsAreIndependent confirms separate clients have
// separate buckets.
func TestRateLimiterDistinctHostsAreIndependent(t *testing.T) {
	rl := newTestLimiter(t, 1, time.Minute)
	h := rl.Middleware(okHandler)

	if c := doRequest(t, h, "203.0.113.1:8000", ""); c != http.StatusOK {
		t.Fatalf("host A first: expected 200, got %d", c)
	}
	// Different host should be its own bucket.
	if c := doRequest(t, h, "203.0.113.2:8001", ""); c != http.StatusOK {
		t.Fatalf("host B first: expected 200, got %d", c)
	}
	// Host A is now over its limit.
	if c := doRequest(t, h, "203.0.113.1:8002", ""); c != http.StatusTooManyRequests {
		t.Fatalf("host A second: expected 429, got %d", c)
	}
}

// TestClientIPStripsPort verifies the host/port extraction helper.
func TestClientIPStripsPort(t *testing.T) {
	cases := []struct {
		remote, want string
	}{
		{"127.0.0.1:54321", "127.0.0.1"},
		{"[::1]:54321", "::1"},
		{"10.0.0.5", "10.0.0.5"}, // no port
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remote
		if got := clientIP(req); got != c.want {
			t.Errorf("clientIP(%q) = %q, want %q", c.remote, got, c.want)
		}
	}
}

// TestRateLimiterTrustedProxySeparatesClients is the regression test for
// issue #129: behind a single reverse proxy every request arrives from the
// proxy's address, so without a trusted-proxy allowlist all tenants share ONE
// bucket and one attacker can rate-limit everyone.
func TestRateLimiterTrustedProxySeparatesClients(t *testing.T) {
	rl, err := NewRateLimiterWithProxies(1, time.Minute, []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	t.Cleanup(rl.Stop)
	h := rl.Middleware(okHandler)

	const proxyPeer = "10.0.0.5:40000"

	// Client A through the proxy: allowed.
	if c := doRequest(t, h, proxyPeer, "203.0.113.10"); c != http.StatusOK {
		t.Fatalf("client A first: expected 200, got %d", c)
	}
	// Client B through the same proxy: must get its OWN bucket.
	if c := doRequest(t, h, proxyPeer, "203.0.113.11"); c != http.StatusOK {
		t.Fatalf("client B first: expected 200 (separate bucket), got %d", c)
	}
	// Client A again: over its own limit.
	if c := doRequest(t, h, proxyPeer, "203.0.113.10"); c != http.StatusTooManyRequests {
		t.Fatalf("client A second: expected 429, got %d", c)
	}
	// Client B again: also over its own limit.
	if c := doRequest(t, h, proxyPeer, "203.0.113.11"); c != http.StatusTooManyRequests {
		t.Fatalf("client B second: expected 429, got %d", c)
	}
}

// A request that does NOT come through a trusted proxy must ignore the
// forwarded header entirely — otherwise a direct attacker could rotate the
// header to mint unlimited buckets.
func TestRateLimiterUntrustedPeerIgnoresForwardedHeader(t *testing.T) {
	rl, err := NewRateLimiterWithProxies(1, time.Minute, []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	t.Cleanup(rl.Stop)
	h := rl.Middleware(okHandler)

	const directPeer = "198.51.100.7:40000"

	if c := doRequest(t, h, directPeer, "203.0.113.10"); c != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", c)
	}
	// A different spoofed XFF value must NOT create a fresh bucket.
	if c := doRequest(t, h, directPeer, "203.0.113.99"); c != http.StatusTooManyRequests {
		t.Fatalf("expected 429 (forwarded header ignored), got %d", c)
	}
}

// A forged left-most XFF entry must not win: the right-most entry that is
// not a trusted proxy is the real client.
func TestClientKeyerUsesRightmostUntrustedForwardedEntry(t *testing.T) {
	k, err := NewClientKeyer([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewClientKeyer: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "192.0.2.66, 203.0.113.20")
	if got := k.ClientKey(req); got != "203.0.113.20" {
		t.Errorf("expected the right-most untrusted entry 203.0.113.20, got %q", got)
	}
}

// TestClientKeyerTruncatesIPv6 proves a client cannot mint unbounded
// buckets by rotating addresses inside its own /64 (issue #137).
func TestClientKeyerTruncatesIPv6(t *testing.T) {
	k, err := NewClientKeyer(nil)
	if err != nil {
		t.Fatalf("NewClientKeyer: %v", err)
	}
	keys := map[string]bool{}
	for _, addr := range []string{"2001:db8:1:2::1", "2001:db8:1:2::2", "2001:db8:1:2:ffff::9"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "[" + addr + "]:1234"
		keys[k.ClientKey(req)] = true
	}
	if len(keys) != 1 {
		t.Errorf("expected all addresses in one /64 to share a bucket, got %d buckets: %v", len(keys), keys)
	}
	// A different /64 must be a different bucket.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[2001:db8:1:3::1]:1234"
	if keys[k.ClientKey(req)] {
		t.Error("a different /64 must map to a different bucket")
	}
}

// TestNewClientKeyerRejectsInvalidCIDR makes a malformed trusted-proxy
// entry a hard error instead of a silently mis-keyed limiter.
func TestNewClientKeyerRejectsInvalidCIDR(t *testing.T) {
	if _, err := NewClientKeyer([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected an error for an invalid trusted proxy CIDR")
	}
}

// TestRateLimiterBoundsKeySpace proves the in-memory limiter's key space is
// capped, so a client that rotates source addresses cannot grow the map
// without bound (issue #137).
func TestRateLimiterBoundsKeySpace(t *testing.T) {
	rl := newTestLimiter(t, 1000, time.Minute)
	h := rl.Middleware(okHandler)

	for i := 0; i < maxRateLimiterEntries+500; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", nil)
		req.RemoteAddr = netaddrForIndex(i)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
	rl.mu.Lock()
	size := len(rl.entries)
	order := len(rl.order)
	rl.mu.Unlock()
	if size > maxRateLimiterEntries {
		t.Errorf("expected at most %d tracked keys, got %d", maxRateLimiterEntries, size)
	}
	if order > maxRateLimiterEntries+1 {
		t.Errorf("expected the insertion order to be bounded too, got %d entries", order)
	}
}

// netaddrForIndex returns a distinct IPv4 address for the given index.
func netaddrForIndex(i int) string {
	return "10." + itoa(i>>16) + "." + itoa((i>>8)&0xff) + "." + itoa(i&0xff) + ":1234"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
