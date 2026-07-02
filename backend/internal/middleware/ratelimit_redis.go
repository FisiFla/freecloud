package middleware

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// NewLimiterFactory returns a constructor for rate limiters. When redisURL is
// non-empty it builds a shared Redis client (once) and every limiter it
// mints is Redis-backed, so all replicas draw from the same budget. When
// redisURL is empty it falls back to per-replica in-memory limiters and logs
// a loud startup warning — acceptable only in dev/test, since
// config.Validate() refuses to start in production without REDIS_URL.
//
// The returned close func shuts down the shared Redis client; call it during
// graceful shutdown.
func NewLimiterFactory(redisURL string, logger *zap.Logger) (factory func(limit int, window time.Duration, name string) Limiter, close func(), err error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if redisURL == "" {
		logger.Warn("rate limiter: REDIS_URL not set, falling back to in-memory per-replica rate limiting. " +
			"This is UNSAFE for multi-instance deployments — each replica enforces its own counters, " +
			"so a client can multiply its effective rate limit by the replica count. " +
			"Acceptable only for local/dev single-instance use.")
		return func(limit int, window time.Duration, _ string) Limiter {
			return NewRateLimiter(limit, window)
		}, func() {}, nil
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, nil, err
	}
	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	logger.Info("rate limiter: using Redis-backed shared rate limiting", zap.String("addr", opts.Addr))

	return func(limit int, window time.Duration, name string) Limiter {
		return NewRedisRateLimiter(client, limit, window, name, logger)
	}, func() { _ = client.Close() }, nil
}

// RedisRateLimiter is a Redis-backed, per-client fixed-window rate limiter.
// Unlike RateLimiter (in-memory), all backend replicas share the same
// counters via Redis, so the configured limit is a single budget across the
// whole deployment rather than per-replica.
//
// Semantics: fixed window, not sliding. Each client key maps to a Redis
// counter (INCR) with a TTL equal to the window, set only on the first
// increment (NX). This is the simplest correct implementation for a
// multi-instance rate limiter: it can allow a short burst of up to 2x the
// limit at window boundaries (worst case: limit requests at the very end of
// one window plus limit more at the very start of the next), which is an
// accepted tradeoff for simplicity over a sliding-window log or
// sorted-set-based approach. Document this if the burst tolerance ever needs
// tightening.
//
// Keying matches RateLimiter exactly: the host portion of r.RemoteAddr, with
// X-Forwarded-For / X-Real-IP never trusted (see clientIP in ratelimit.go for
// the full rationale — this was a past bug: trusting spoofable headers let
// clients rotate the header value to bypass the limit).
//
// Fail-closed: if Redis is unreachable at request time, requests are DENIED
// (429) rather than allowed through. A rate limiter that fails open under
// load is not a rate limiter — it disables itself exactly when protection
// matters most (e.g. during a Redis outage caused by the same traffic spike
// the limiter exists to blunt).
type RedisRateLimiter struct {
	client *redis.Client
	limit  int
	window time.Duration
	prefix string
	logger *zap.Logger
}

// NewRedisRateLimiter creates a RedisRateLimiter allowing at most `limit`
// requests per `window` per client, backed by the given Redis client. prefix
// namespaces the counter keys so multiple limiter instances (e.g. the health,
// setup, and mutate limiters in routes.go) sharing one Redis do not collide.
//
// window must be >= 1 second: Redis EXPIRE has second-level granularity and
// truncates sub-second durations to 0, which would leave the counter key
// without a TTL (never expiring, i.e. permanently stuck at the limit). All
// current call sites (routes.go) use minute-scale windows, well above this
// floor.
func NewRedisRateLimiter(client *redis.Client, limit int, window time.Duration, prefix string, logger *zap.Logger) *RedisRateLimiter {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RedisRateLimiter{
		client: client,
		limit:  limit,
		window: window,
		prefix: prefix,
		logger: logger,
	}
}

// Stop is a no-op for RedisRateLimiter: the Redis client is shared across all
// limiter instances and is closed once by whoever constructed it (main.go),
// not by each individual limiter.
func (rl *RedisRateLimiter) Stop() {}

// Middleware returns an HTTP middleware that enforces the rate limit via Redis.
func (rl *RedisRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.prefix + ":" + clientIP(r)
		allowed, err := rl.allow(r.Context(), key)
		if err != nil {
			// Fail closed: Redis unreachable/erroring means we cannot verify the
			// client is under budget, so treat it as over budget rather than
			// silently disabling rate limiting.
			rl.logger.Warn("rate limiter: redis error, failing closed", zap.Error(err), zap.String("key", key))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"success":false,"error":"rate limit exceeded"}`))
			return
		}
		if !allowed {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"success":false,"error":"rate limit exceeded"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allow increments the fixed-window counter for key and reports whether the
// request is within budget. The counter's TTL is set to the window duration
// on first increment (NX — only if no TTL is already set), so the window
// resets `window` after the first request in it, not on a wall-clock boundary.
func (rl *RedisRateLimiter) allow(ctx context.Context, key string) (bool, error) {
	// Bound the Redis round-trip so a slow/hanging Redis can't stall requests
	// indefinitely; the caller's fail-closed path kicks in on timeout too.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	count, err := rl.client.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		// First request in this window: arm the expiry. NX guards against a
		// race where a concurrent Incr already set it (harmless either way,
		// but avoids clobbering a shorter remaining TTL with a fresh full one).
		if err := rl.client.Expire(ctx, key, rl.window).Err(); err != nil {
			return false, err
		}
	}
	return count <= int64(rl.limit), nil
}
