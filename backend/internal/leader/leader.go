// Package leader provides pg_advisory_lock-based leader election so
// background jobs (reconcile, audit retention, analytics snapshot) run on
// exactly one backend replica at a time (B3, v1.7 HA — ADR 0003's
// single-instance cap covered these jobs implicitly by only ever running one
// process; multi-instance needs an explicit leader).
//
// Design: a session-scoped pg_advisory_lock is acquired on a single dedicated
// connection (NOT the shared query pool — a session lock is tied to the
// connection that took it, so the connection must be held open for as long
// as leadership is held) and never explicitly released while healthy. If the
// connection drops (crash, network partition, pool recycling it) Postgres
// releases the lock automatically, and another instance's retry loop picks
// it up — this is what makes failover work without a heartbeat protocol of
// our own.
package leader

import (
	"context"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// Row is the minimal pgx.Row surface the elector needs (Scan). Defined
// locally so fakes don't need to import pgx.
type Row interface {
	Scan(dest ...any) error
}

// PoolConn is the subset of *pgxpool.Conn the elector uses. Defined as an
// interface so unit tests can inject a fake connection without a real
// Postgres; *pgxpool.Conn satisfies it directly (see PoolAdapter below).
type PoolConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) Row
	Ping(ctx context.Context) error
	Release()
}

// Pool is the subset of *pgxpool.Pool the elector uses to obtain a dedicated
// connection to hold the session-scoped advisory lock on. *pgxpool.Pool
// satisfies it via PoolAdapter.
type Pool interface {
	Acquire(ctx context.Context) (PoolConn, error)
}

var isLeaderGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "freecloud_leader_election_is_leader",
	Help: "1 if this backend instance currently holds the named leadership lock, 0 otherwise.",
}, []string{"job"})

// Elector runs a retry-with-jitter loop trying to acquire a named
// session-scoped pg_advisory_lock. While it holds the lock it is "the
// leader" for that job name; callers gate background-job execution on
// IsLeader() or use Run(), which only invokes the supplied function while
// leadership is held.
//
// The loop itself (tryAcquire/releaseIfLeader/conn) is only ever touched by
// the single goroutine started by Start, so it needs no locking. isLeader is
// the one field read from other goroutines (by IsLeader/Run callers on the
// ticker goroutines of reconcile/audit/snapshot), so it is an atomic.Bool.
type Elector struct {
	pool     Pool
	job      string
	lockID   int64
	logger   *zap.Logger
	interval time.Duration // base retry/re-check interval

	conn     PoolConn // owned by the loop goroutine only; non-nil while leader
	isLeader atomic.Bool
}

// New creates an Elector for the given job name and advisory lock id. Each
// background job (reconcile, audit-retention, snapshot, ...) must use a
// distinct lockID — collisions would make unrelated jobs share leadership,
// which is harmless correctness-wise (both would simply run on the same
// instance) but defeats independent failover, so keep them distinct.
func New(pool Pool, job string, lockID int64, logger *zap.Logger) *Elector {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Elector{
		pool:     pool,
		job:      job,
		lockID:   lockID,
		logger:   logger.With(zap.String("leader_job", job)),
		interval: 5 * time.Second,
	}
}

// WithInterval overrides the default 5s acquire-retry / leader-health-check
// interval. Intended for tests; production call sites use the default.
func (e *Elector) WithInterval(d time.Duration) *Elector {
	e.interval = d
	return e
}

// IsLeader reports whether this instance currently holds the lock.
func (e *Elector) IsLeader() bool {
	return e.isLeader.Load()
}

// Start launches the acquire/hold/retry loop in a goroutine. It returns
// immediately; the goroutine stops (releasing the lock if held) when ctx is
// cancelled.
func (e *Elector) Start(ctx context.Context) {
	go e.loop(ctx)
}

// loop repeatedly tries to become leader; once leader, it holds the
// connection open and periodically pings it (Ping doubles as both a
// liveness check and the mechanism by which a dead connection is detected
// promptly rather than only on the next query). On any failure it releases
// state and returns to the acquire phase after a jittered backoff.
func (e *Elector) loop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			e.releaseIfLeader()
			return
		}

		if !e.isLeader.Load() {
			if !e.tryAcquire(ctx) {
				if !e.sleepWithJitter(ctx) {
					return
				}
				continue
			}
		}

		// Leader (or just became leader): wait one interval, then verify the
		// connection is still alive before the next iteration.
		if !e.sleepWithJitter(ctx) {
			e.releaseIfLeader()
			return
		}
		if e.isLeader.Load() {
			if err := e.conn.Ping(ctx); err != nil {
				e.logger.Warn("leader: lost connection holding advisory lock, will retry acquisition", zap.Error(err))
				e.releaseIfLeader()
			}
		}
	}
}

// tryAcquire attempts a non-blocking pg_try_advisory_lock on a fresh
// dedicated connection. Returns true and stores the connection on success.
func (e *Elector) tryAcquire(ctx context.Context) bool {
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		e.logger.Warn("leader: failed to acquire pool connection", zap.Error(err))
		return false
	}

	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, e.lockID).Scan(&got); err != nil {
		e.logger.Warn("leader: pg_try_advisory_lock query failed", zap.Error(err))
		conn.Release()
		return false
	}
	if !got {
		// Another instance holds it — release this connection back to the pool
		// immediately rather than holding it idle until the next retry.
		conn.Release()
		return false
	}

	e.conn = conn
	e.isLeader.Store(true)
	isLeaderGauge.WithLabelValues(e.job).Set(1)
	e.logger.Info("leader: acquired leadership")
	return true
}

// releaseIfLeader releases the advisory lock (implicitly, by releasing the
// connection back to the pool — a session-scoped lock is held only as long
// as its session/connection lives) and clears leader state.
func (e *Elector) releaseIfLeader() {
	if !e.isLeader.Load() {
		return
	}
	e.isLeader.Store(false)
	isLeaderGauge.WithLabelValues(e.job).Set(0)
	if e.conn != nil {
		e.conn.Release()
		e.conn = nil
	}
	e.logger.Info("leader: released leadership")
}

// sleepWithJitter waits interval ± 20% jitter, or returns false immediately
// if ctx is cancelled first. Jitter avoids every replica retrying in
// lockstep after a leader dies.
func (e *Elector) sleepWithJitter(ctx context.Context) bool {
	spread := int64(e.interval) / 5 // 20%
	if spread <= 0 {
		spread = 1
	}
	jitter := time.Duration(rand.Int63n(spread))
	wait := e.interval - time.Duration(spread)/2 + jitter
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

// Run invokes fn only while this instance holds leadership; otherwise it
// no-ops. Convenience for wrapping an existing ticker callback so the whole
// job body (side effects, DB writes) only executes on the leader.
func (e *Elector) Run(ctx context.Context, fn func(ctx context.Context)) {
	if !e.IsLeader() {
		e.logger.Debug("leader: skipping job run, not leader")
		return
	}
	fn(ctx)
}

// PoolAdapter wraps a *pgxpool.Pool to satisfy Pool. Use in main.go:
//
//	elector := leader.New(leader.PoolAdapter{Pool: pool}, "reconcile", lockID, logger)
type PoolAdapter struct {
	Pool *pgxpool.Pool
}

func (a PoolAdapter) Acquire(ctx context.Context) (PoolConn, error) {
	conn, err := a.Pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	// pgxpool.Conn.QueryRow returns the concrete pgx.Row type, not this
	// package's local Row interface, so Go's exact-return-type rule blocks a
	// direct interface conversion even though pgx.Row also has Scan(...).
	// Wrap it in a thin shim that re-declares QueryRow's return as Row.
	return connShim{conn}, nil
}

// connShim adapts *pgxpool.Conn to PoolConn.
type connShim struct {
	conn *pgxpool.Conn
}

func (c connShim) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return c.conn.QueryRow(ctx, sql, args...)
}

func (c connShim) Ping(ctx context.Context) error {
	return c.conn.Ping(ctx)
}

func (c connShim) Release() {
	c.conn.Release()
}
