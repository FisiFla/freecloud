package middleware

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limiter is satisfied by any rate-limiter implementation that can be wired
// into the HTTP middleware chain. RateLimiter (in-memory) and
// RedisRateLimiter (backend/internal/middleware/ratelimit_redis.go) both
// implement it, so callers (routes.go) can select an implementation without
// changing call sites.
type Limiter interface {
	Middleware(next http.Handler) http.Handler
	Stop()
}

// rateLimitEntry tracks request timestamps for a single client key.
type rateLimitEntry struct {
	mu    sync.Mutex
	times []time.Time
}

// maxRateLimiterEntries caps the number of tracked client keys in the
// in-memory limiter.
//
// Without a cap, a client with an IPv6 /64 (or any attacker able to source
// many addresses) can mint an unbounded number of buckets: every distinct key
// gets its own entry, and the GC only removes entries idle for a full window.
// The cap bounds the memory the limiter can consume (issue #137).
const maxRateLimiterEntries = 10000

// RateLimiter is a simple in-memory, per-client sliding-window rate limiter.
// It is suitable for a single-instance deployment: each replica keeps its own
// counters, so with N replicas a client can burst to N times the configured
// limit by round-robining across them. For multi-instance deployments use
// RedisRateLimiter instead, which shares one counter across all replicas.
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	// order lists the keys in insertion order, so the oldest entry can be
	// evicted in O(1) when the key-space cap is reached.
	order  []string
	limit  int
	window time.Duration
	keyer  *ClientKeyer
	cancel context.CancelFunc
}

// NewRateLimiter creates a RateLimiter allowing at most `limit` requests per
// `window` per client. The background GC goroutine is tied to the returned
// cancel func via Stop().
//
// The client key is derived from the peer address only; use
// NewRateLimiterWithProxies to trust the X-Forwarded-For header of a known
// reverse proxy.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl, err := NewRateLimiterWithProxies(limit, window, nil)
	if err != nil {
		// A nil proxy list cannot fail to parse.
		panic("middleware: NewRateLimiter: " + err.Error())
	}
	return rl
}

// NewRateLimiterWithProxies creates a RateLimiter that derives the client key
// with the given trusted-proxy configuration.
//
// trustedProxies lists the reverse proxies whose X-Forwarded-For header may
// be trusted; see NewClientKeyer. Pass nil to key on the peer address only
// (the safe default).
func NewRateLimiterWithProxies(limit int, window time.Duration, trustedProxies []string) (*RateLimiter, error) {
	keyer, err := NewClientKeyer(trustedProxies)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	rl := &RateLimiter{
		entries: make(map[string]*rateLimitEntry),
		limit:   limit,
		window:  window,
		keyer:   keyer,
		cancel:  cancel,
	}
	go rl.gc(ctx)
	return rl, nil
}

// Stop halts the background GC goroutine. Safe to call multiple times.
func (rl *RateLimiter) Stop() {
	rl.cancel()
}

// Middleware returns an HTTP middleware that enforces the rate limit.
//
// The client is identified by clientKey, which derives the client address
// from the request. When the request arrives through a reverse proxy that is
// listed in the trusted-proxy set, the forwarded client address is used;
// otherwise the peer address is used and the X-Forwarded-For header is
// ignored, because it is trivially spoofable by the client (see clientKey).
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.keyer.ClientKey(r)
		if !rl.allow(key) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"success":false,"error":"rate limit exceeded"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allow reports whether a request from the given key is permitted, recording
// the timestamp if so.
func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	entry, ok := rl.entries[key]
	if !ok {
		// Bound the key space: evict the oldest entries when the cap is
		// reached, so a client that rotates addresses cannot grow the map
		// without bound (issue #137).
		for len(rl.entries) >= maxRateLimiterEntries {
			if !rl.evictOldestLocked() {
				break
			}
		}
		entry = &rateLimitEntry{}
		rl.entries[key] = entry
		rl.order = append(rl.order, key)
	}
	rl.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Drop timestamps outside the window.
	kept := entry.times[:0]
	for _, t := range entry.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= rl.limit {
		entry.times = kept
		return false
	}

	entry.times = append(kept, now)
	return true
}

// evictOldestLocked removes the oldest tracked key and reports whether one
// was removed. The caller must hold rl.mu.
//
// The insertion order is kept in rl.order, so eviction is O(1) amortised: a
// flood of distinct source addresses must not turn the cap into a CPU
// amplifier (issue #137).
func (rl *RateLimiter) evictOldestLocked() bool {
	for len(rl.order) > 0 {
		k := rl.order[0]
		rl.order = rl.order[1:]
		if _, ok := rl.entries[k]; ok {
			delete(rl.entries, k)
			return true
		}
		// The key was already removed (e.g. by the GC); skip it.
	}
	return false
}

// gc periodically removes entries that haven't been touched within the window.
func (rl *RateLimiter) gc(ctx context.Context) {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-rl.window)
			for k, entry := range rl.entries {
				entry.mu.Lock()
				if len(entry.times) == 0 || !entry.times[len(entry.times)-1].After(cutoff) {
					delete(rl.entries, k)
				}
				entry.mu.Unlock()
			}
			// Drop the deleted keys from the insertion order so it does
			// not grow without bound either.
			if len(rl.entries) == 0 {
				rl.order = rl.order[:0]
			} else {
				kept := rl.order[:0]
				for _, k := range rl.order {
					if _, ok := rl.entries[k]; ok {
						kept = append(kept, k)
					}
				}
				rl.order = kept
			}
			rl.mu.Unlock()
		}
	}
}

// clientIP extracts the host portion of r.RemoteAddr, dropping the source port
// so that reconnects from the same client collapse to one key.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port present (e.g. a unix socket or already-host-only value).
		return r.RemoteAddr
	}
	return host
}
