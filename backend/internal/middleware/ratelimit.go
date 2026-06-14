package middleware

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimitEntry tracks request timestamps for a single client key.
type rateLimitEntry struct {
	mu    sync.Mutex
	times []time.Time
}

// RateLimiter is a simple in-memory, per-client sliding-window rate limiter.
// It is suitable for a single-instance deployment. For multi-instance setups,
// replace the storage with Redis or similar.
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
// The client is identified by r.RemoteAddr only. X-Forwarded-For is NOT
// trusted because it is trivially spoofable by the client and would allow
// bypassing the limit by rotating the header value per request. If you run
// behind a trusted proxy, configure chi's RealIP middleware with an explicit
// proxy allowlist instead.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(r.RemoteAddr) {
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
