package middleware

import (
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// newTestRedisLimiter spins up an in-process miniredis server (no Docker
// needed) and returns a RedisRateLimiter backed by it, plus a cleanup func.
func newTestRedisLimiter(t *testing.T, limit int, window time.Duration) (*RedisRateLimiter, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	return NewRedisRateLimiter(client, limit, window, "test", zap.NewNop()), mr
}

func TestRedisRateLimiterAllowsUpToLimit(t *testing.T) {
	rl, _ := newTestRedisLimiter(t, 2, time.Minute)
	h := rl.Middleware(okHandler)

	if c := doRequest(t, h, "127.0.0.1:51000", ""); c != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", c)
	}
	if c := doRequest(t, h, "127.0.0.1:51001", ""); c != http.StatusOK {
		t.Fatalf("second request (new port, same host): expected 200, got %d", c)
	}
	if c := doRequest(t, h, "127.0.0.1:51002", ""); c != http.StatusTooManyRequests {
		t.Fatalf("third request (over limit): expected 429, got %d", c)
	}
}

// TestRedisRateLimiterSharesStateAcrossInstances is the core HA property:
// two *separate* RedisRateLimiter instances (standing in for two backend
// replicas) pointed at the same Redis must share one budget, not one each.
func TestRedisRateLimiterSharesStateAcrossInstances(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	replicaA := NewRedisRateLimiter(client, 2, time.Minute, "shared", zap.NewNop())
	replicaB := NewRedisRateLimiter(client, 2, time.Minute, "shared", zap.NewNop())
	hA := replicaA.Middleware(okHandler)
	hB := replicaB.Middleware(okHandler)

	// Two requests split across "replicas" consume the shared budget of 2.
	if c := doRequest(t, hA, "10.0.0.1:1000", ""); c != http.StatusOK {
		t.Fatalf("replica A request 1: expected 200, got %d", c)
	}
	if c := doRequest(t, hB, "10.0.0.1:1001", ""); c != http.StatusOK {
		t.Fatalf("replica B request 1: expected 200, got %d", c)
	}
	// Third request, regardless of which replica serves it, must be denied —
	// if each replica had its own counter this would incorrectly succeed.
	if c := doRequest(t, hA, "10.0.0.1:1002", ""); c != http.StatusTooManyRequests {
		t.Fatalf("replica A request 2 (over shared limit): expected 429, got %d", c)
	}
	if c := doRequest(t, hB, "10.0.0.1:1003", ""); c != http.StatusTooManyRequests {
		t.Fatalf("replica B request 2 (over shared limit): expected 429, got %d", c)
	}
}

func TestRedisRateLimiterIgnoresForwardedHeader(t *testing.T) {
	rl, _ := newTestRedisLimiter(t, 1, time.Minute)
	h := rl.Middleware(okHandler)

	if c := doRequest(t, h, "172.16.0.1:7000", "9.9.9.9"); c != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", c)
	}
	// Spoofed XFF must not create a fresh bucket — keying matches the
	// in-memory limiter's clientIP-only behavior exactly.
	if c := doRequest(t, h, "172.16.0.1:7001", "8.8.8.8"); c != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF should not bypass limit: expected 429, got %d", c)
	}
}

func TestRedisRateLimiterDistinctHostsAreIndependent(t *testing.T) {
	rl, _ := newTestRedisLimiter(t, 1, time.Minute)
	h := rl.Middleware(okHandler)

	if c := doRequest(t, h, "203.0.113.1:8000", ""); c != http.StatusOK {
		t.Fatalf("host A first: expected 200, got %d", c)
	}
	if c := doRequest(t, h, "203.0.113.2:8001", ""); c != http.StatusOK {
		t.Fatalf("host B first: expected 200, got %d", c)
	}
	if c := doRequest(t, h, "203.0.113.1:8002", ""); c != http.StatusTooManyRequests {
		t.Fatalf("host A second: expected 429, got %d", c)
	}
}

func TestRedisRateLimiterWindowExpiry(t *testing.T) {
	// Redis EXPIRE has second-level granularity (sub-second durations round
	// down to 0, i.e. no expiry) — use a 1s window, the minimum meaningful
	// value, rather than the sub-second windows the in-memory limiter's
	// equivalent test uses.
	rl, mr := newTestRedisLimiter(t, 1, time.Second)
	h := rl.Middleware(okHandler)

	if c := doRequest(t, h, "192.168.1.1:6000", ""); c != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", c)
	}
	if c := doRequest(t, h, "192.168.1.1:6001", ""); c != http.StatusTooManyRequests {
		t.Fatalf("second (within window): expected 429, got %d", c)
	}
	// miniredis doesn't advance TTLs with real wall-clock time; fast-forward
	// its internal clock past the window instead of sleeping.
	mr.FastForward(2 * time.Second)
	if c := doRequest(t, h, "192.168.1.1:6002", ""); c != http.StatusOK {
		t.Fatalf("after window expiry: expected 200, got %d", c)
	}
}

// TestRedisRateLimiterFailsClosedOnRedisDown proves the fail-closed contract:
// once Redis becomes unreachable, requests are DENIED, not silently allowed.
func TestRedisRateLimiterFailsClosedOnRedisDown(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	rl := NewRedisRateLimiter(client, 100, time.Minute, "downtest", zap.NewNop())
	h := rl.Middleware(okHandler)

	// Sanity: works while Redis is up, well under the generous limit.
	if c := doRequest(t, h, "127.0.0.1:9000", ""); c != http.StatusOK {
		t.Fatalf("expected 200 while redis is up, got %d", c)
	}

	// Simulate Redis becoming unreachable.
	mr.Close()

	if c := doRequest(t, h, "127.0.0.1:9001", ""); c != http.StatusTooManyRequests {
		t.Fatalf("expected fail-closed 429 when redis is down, got %d", c)
	}
}

func TestRedisRateLimiterKeyNamespacing(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	// Two limiters with different name/prefix must not share buckets even for
	// the same client — mirrors routes.go having independent per-route limiters.
	limiterA := NewRedisRateLimiter(client, 1, time.Minute, "route_a", zap.NewNop())
	limiterB := NewRedisRateLimiter(client, 1, time.Minute, "route_b", zap.NewNop())
	hA := limiterA.Middleware(okHandler)
	hB := limiterB.Middleware(okHandler)

	if c := doRequest(t, hA, "10.1.1.1:1000", ""); c != http.StatusOK {
		t.Fatalf("route A first: expected 200, got %d", c)
	}
	// Route B's bucket for the same client is independent — must still allow.
	if c := doRequest(t, hB, "10.1.1.1:1001", ""); c != http.StatusOK {
		t.Fatalf("route B first (different namespace): expected 200, got %d", c)
	}
}
