package middleware

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// incrWithExpiryScript increments the counter and arms its TTL atomically.
//
// The previous implementation issued INCR and then a separate EXPIRE. If the
// connection dropped between the two commands, the counter was left without a
// TTL: the key never expired and that client was permanently rate limited
// (issue #136). Doing both in one Lua script makes the pair atomic — Redis
// executes the whole script without interleaving other commands — so the
// counter ALWAYS has a TTL.
//
// The script also re-arms the TTL whenever the key has none (PTTL < 0). Redis
// does NOT roll back the writes of a script that fails halfway, so a PEXPIRE
// that errored would still leave the incremented key without an expiry; the
// next request repairs it instead of leaving the client blocked forever.
//
// The TTL is set with PEXPIRE so sub-second windows are supported (Redis's
// EXPIRE only has second granularity, and a sub-second window would otherwise
// expire the key immediately).
var incrWithExpiryScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return current
`)

// NewLimiterFactory returns a constructor for rate limiters. When redisURL is
// non-empty it builds a shared Redis client (once) and every limiter it
// mints is Redis-backed, so all replicas draw from the same budget. When
// redisURL is empty it falls back to per-replica in-memory limiters and logs
// a loud startup warning — acceptable only in dev/test, since
// config.Validate() refuses to start in production without REDIS_URL.
//
// trustedProxies lists the reverse proxies whose X-Forwarded-For header may
// be trusted when deriving the client key; see NewClientKeyer. Without it,
// every request behind a single reverse proxy shares one bucket (issue #129).
//
// The returned close func shuts down the shared Redis client; call it during
// graceful shutdown.
func NewLimiterFactory(redisURL string, trustedProxies []string, logger *zap.Logger) (factory func(limit int, window time.Duration, name string) Limiter, close func(), err error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	// Validate the trusted-proxy configuration up front so a malformed CIDR
	// is a startup error rather than a silently mis-keyed limiter.
	if _, err := NewClientKeyer(trustedProxies); err != nil {
		return nil, nil, err
	}
	if redisURL == "" {
		logger.Warn("rate limiter: REDIS_URL not set, falling back to in-memory per-replica rate limiting. " +
			"This is UNSAFE for multi-instance deployments — each replica enforces its own counters, " +
			"so a client can multiply its effective rate limit by the replica count. " +
			"Acceptable only for local/dev single-instance use.")
		return func(limit int, window time.Duration, _ string) Limiter {
			rl, err := NewRateLimiterWithProxies(limit, window, trustedProxies)
			if err != nil {
				// The CIDRs were validated by NewClientKeyer above, so this
				// cannot fail; fall back to a keyer-less limiter rather than
				// panicking in a request path.
				logger.Error("rate limiter: building in-memory limiter failed", zap.Error(err))
				rl = NewRateLimiter(limit, window)
			}
			return rl
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
		return NewRedisRateLimiter(client, limit, window, name, logger, trustedProxies)
	}, func() { _ = client.Close() }, nil
}

// RedisRateLimiter is a Redis-backed, per-client fixed-window rate limiter.
// Unlike RateLimiter (in-memory), all backend replicas share the same
// counters via Redis, so the configured limit is one budget across the whole
// deployment, not per-replica.
//
// Semantics: fixed window, not sliding. Each client key maps to a Redis
// counter (INCR) with a TTL equal to the window, set atomically together with
// the first increment (see incrWithExpiryScript). This is the simplest
// correct implementation for a multi-instance rate limiter: it can allow a
// short burst of up to 2x the limit at window boundaries (worst case: limit
// requests at the very end of one window plus limit more at the very start of
// the next), which is an accepted tradeoff for simplicity over a sliding-window
// log or sorted-set-based approach. Document this if the burst tolerance ever
// needs tightening.
//
// Keying matches the in-memory limiter exactly: the client address derived
// by ClientKeyer, with X-Forwarded-For only trusted when the peer is a
// configured trusted proxy.
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
	keyer  *ClientKeyer
	logger *zap.Logger
}

// NewRedisRateLimiter creates a RedisRateLimiter allowing at most `limit`
// requests per `window` per client, backed by the given Redis client. prefix
// namespaces the counter keys so multiple limiter instances (e.g. the health,
// setup, and mutate limiters in routes.go) sharing one Redis do not collide.
//
// trustedProxies optionally lists the reverse proxies whose X-Forwarded-For
// header may be trusted when deriving the client key; see NewClientKeyer.
// Omitting it keys on the peer address only (the safe default).
func NewRedisRateLimiter(client *redis.Client, limit int, window time.Duration, prefix string, logger *zap.Logger, trustedProxies ...[]string) *RedisRateLimiter {
	if logger == nil {
		logger = zap.NewNop()
	}
	var proxies []string
	if len(trustedProxies) > 0 {
		proxies = trustedProxies[0]
	}
	keyer, err := NewClientKeyer(proxies)
	if err != nil {
		// The CIDRs were validated when the factory was built, so this
		// cannot fail in practice. Fall back to peer-address keying rather
		// than panicking in a request path.
		logger.Error("rate limiter: invalid trusted proxy configuration", zap.Error(err))
		keyer, _ = NewClientKeyer(nil)
	}
	return &RedisRateLimiter{
		client: client,
		limit:  limit,
		window: window,
		prefix: prefix,
		keyer:  keyer,
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
		key := rl.prefix + ":" + rl.keyer.ClientKey(r)
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
// request is within budget. The counter's TTL is armed atomically with the
// first increment, so the key can never be left without one.
func (rl *RedisRateLimiter) allow(ctx context.Context, key string) (bool, error) {
	// Bound the Redis round-trip so a slow/hanging Redis can't stall requests
	// indefinitely; the caller's fail-closed path kicks in on timeout too.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	count, err := incrWithExpiryScript.Run(ctx, rl.client, []string{key}, rl.window.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return count <= int64(rl.limit), nil
}
