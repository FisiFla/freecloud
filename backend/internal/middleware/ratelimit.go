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

// RateLimiter is a simple in-memory, per-client sliding-window rate limiter.
// It is suitable for a single-instance deployment: each replica keeps its own
// counters, so with N replicas a client can burst to N times the configured
// limit by round-robining across them. For multi-instance deployments use
// RedisRateLimiter instead, which shares one counter across all replicas.
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	limit   int
	window  time.Duration
	cancel  context.CancelFunc
}

// NewRateLimiter creates a RateLimiter allowing at most `limit` requests per
// `window` per client. The background GC goroutine is tied to the returned
// cancel func via Stop().
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	ctx, cancel := context.WithCancel(context.Background())
	rl := &RateLimiter{
		entries: make(map[string]*rateLimitEntry),
		limit:   limit,
		window:  window,
		cancel:  cancel,
	}
	go rl.gc(ctx)
	return rl
}

// Stop halts the background GC goroutine. Safe to call multiple times.
func (rl *RateLimiter) Stop() {
	rl.cancel()
}

// Middleware returns an HTTP middleware that enforces the rate limit.
//
// The client is identified by the host portion of r.RemoteAddr only (the port
// is stripped, since a single client reconnects with a new source port each
// time). X-Forwarded-For / X-Real-IP are NOT trusted because they are
// trivially spoofable by the client and would allow bypassing the limit by
// rotating the header value per request.
//
// NOTE: this assumes RealIP-style middleware is NOT installed globally. If you
// later add a trusted-proxy layer, key derivation must move behind it.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientIP(r)
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

// allow reports whether a request from the given key is permitted, recording
// the timestamp if so.
func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	entry, ok := rl.entries[key]
	if !ok {
		entry = &rateLimitEntry{}
		rl.entries[key] = entry
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
			rl.mu.Unlock()
		}
	}
}
