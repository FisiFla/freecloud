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

// TestRateLimiterSameHostDifferentPorts collapses to one bucket.
func TestRateLimiterSameHostDifferentPorts(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	defer rl.Stop()
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
	rl := NewRateLimiter(1, time.Minute)
	defer rl.Stop()
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
	rl := NewRateLimiter(1, 30*time.Millisecond)
	defer rl.Stop()
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
// X-Forwarded-For does not change the rate-limit key.
func TestRateLimiterIgnoresForwardedHeader(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)
	defer rl.Stop()
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
	rl := NewRateLimiter(1, time.Minute)
	defer rl.Stop()
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
