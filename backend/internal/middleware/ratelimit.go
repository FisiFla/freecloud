package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimitEntry tracks request timestamps for a single client key.
type rateLimitEntry struct {
	mu       sync.Mutex
	times    []time.Time
}

// RateLimiter is a simple in-memory, per-client sliding-window rate limiter.
// It is suitable for a single-instance deployment. For multi-instance setups,
// replace the storage with Redis or similar.
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	limit   int
	window  time.Duration
}

// NewRateLimiter creates a RateLimiter allowing at most `limit` requests per
// `window` per client (identified by remote address).
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*rateLimitEntry),
		limit:   limit,
		window:  window,
	}
	// Periodically prune stale entries so the map doesn't grow unbounded.
	go rl.gc()
	return rl
}

// Middleware returns an HTTP middleware that enforces the rate limit.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use the forwarded-for or remote addr as the client key.
		key := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			key = xff
		}

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
func (rl *RateLimiter) gc() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for range ticker.C {
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
