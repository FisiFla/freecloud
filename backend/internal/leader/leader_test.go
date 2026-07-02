package leader

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// fakeLockServer simulates a single Postgres advisory-lock namespace shared
// by multiple fakePool "connections", so tests can exercise two competing
// Electors without a real database. Only one fakeConn may hold a given
// lockID at a time, mirroring pg_try_advisory_lock semantics.
type fakeLockServer struct {
	mu      sync.Mutex
	holders map[int64]*fakeConn // lockID -> current holder, nil entry means free
}

func newFakeLockServer() *fakeLockServer {
	return &fakeLockServer{holders: make(map[int64]*fakeConn)}
}

func (s *fakeLockServer) tryLock(lockID int64, c *fakeConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if holder, ok := s.holders[lockID]; ok && holder != nil {
		return false
	}
	s.holders[lockID] = c
	return true
}

func (s *fakeLockServer) release(lockID int64, c *fakeConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.holders[lockID] == c {
		delete(s.holders, lockID)
	}
}

// fakePool is a Pool backed by a shared fakeLockServer. Each Acquire call
// returns a fresh fakeConn, mirroring a real pool handing out connections
// from a shared pool of backend sessions.
type fakePool struct {
	server *fakeLockServer
	// dead, when non-nil and true, makes every new connection immediately
	// report Ping failures — simulates "this instance can no longer reach
	// Postgres". *atomic.Bool (not plain bool) because the test goroutine
	// flips it while the Elector's loop goroutine concurrently reads it.
	dead *atomic.Bool
}

func (p *fakePool) Acquire(ctx context.Context) (PoolConn, error) {
	if p.dead != nil && p.dead.Load() {
		return nil, errors.New("connection refused (simulated)")
	}
	return &fakeConn{server: p.server, dead: p.dead}, nil
}

// fakeConn simulates one dedicated session connection.
type fakeConn struct {
	server   *fakeLockServer
	dead     *atomic.Bool
	held     map[int64]bool
	released bool
}

func (c *fakeConn) QueryRow(ctx context.Context, sql string, args ...any) Row {
	lockID, _ := args[0].(int64)
	got := false
	if c.dead == nil || !c.dead.Load() {
		got = c.server.tryLock(lockID, c)
		if got {
			if c.held == nil {
				c.held = make(map[int64]bool)
			}
			c.held[lockID] = true
		}
	}
	return fakeRow{val: got}
}

func (c *fakeConn) Ping(ctx context.Context) error {
	if c.dead != nil && c.dead.Load() {
		return errors.New("connection lost (simulated)")
	}
	return nil
}

func (c *fakeConn) Release() {
	if c.released {
		return
	}
	c.released = true
	for lockID := range c.held {
		c.server.release(lockID, c)
	}
}

type fakeRow struct{ val bool }

func (r fakeRow) Scan(dest ...any) error {
	*(dest[0].(*bool)) = r.val
	return nil
}

// waitFor polls until cond() is true or the timeout elapses, failing the test
// on timeout. Used instead of a fixed sleep so tests aren't flaky under load.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met after %s", timeout)
}

func TestElectorSingleInstanceBecomesLeader(t *testing.T) {
	server := newFakeLockServer()
	pool := &fakePool{server: server}
	e := New(pool, "test-job", 42, zap.NewNop()).WithInterval(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Start(ctx)

	waitFor(t, time.Second, e.IsLeader)
}

// TestElectorOnlyOneOfTwoCompetesToLeadership is the core B3 property: two
// Electors racing for the same lockID must never both report IsLeader() true
// at once.
func TestElectorOnlyOneOfTwoCompetesToLeadership(t *testing.T) {
	server := newFakeLockServer()
	poolA := &fakePool{server: server}
	poolB := &fakePool{server: server}

	eA := New(poolA, "test-job", 42, zap.NewNop()).WithInterval(15 * time.Millisecond)
	eB := New(poolB, "test-job", 42, zap.NewNop()).WithInterval(15 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eA.Start(ctx)
	eB.Start(ctx)

	waitFor(t, time.Second, func() bool { return eA.IsLeader() || eB.IsLeader() })

	// Give the loser a few more ticks to (incorrectly) also become leader, if
	// the mutual exclusion were broken.
	time.Sleep(150 * time.Millisecond)

	if eA.IsLeader() && eB.IsLeader() {
		t.Fatal("both electors report leadership simultaneously — mutual exclusion violated")
	}
	if !eA.IsLeader() && !eB.IsLeader() {
		t.Fatal("neither elector became leader")
	}
}

// TestElectorFailsOverWhenLeaderConnectionDies proves the failover story:
// when the leader's connection dies, the other instance takes over.
func TestElectorFailsOverWhenLeaderConnectionDies(t *testing.T) {
	server := newFakeLockServer()
	var aDead atomic.Bool
	poolA := &fakePool{server: server, dead: &aDead}
	poolB := &fakePool{server: server}

	eA := New(poolA, "test-job", 7, zap.NewNop()).WithInterval(15 * time.Millisecond)
	eB := New(poolB, "test-job", 7, zap.NewNop()).WithInterval(15 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eA.Start(ctx)
	// Let A win the race for leadership (B starts after a small delay).
	waitFor(t, time.Second, eA.IsLeader)
	eB.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	if eB.IsLeader() {
		t.Fatal("B should not be leader while A holds the lock")
	}

	// Kill A's connection — its next Ping should fail, releasing the lock.
	aDead.Store(true)

	waitFor(t, 2*time.Second, eB.IsLeader)
	waitFor(t, 2*time.Second, func() bool { return !eA.IsLeader() })
}

// TestElectorRunOnlyExecutesWhenLeader confirms the Run() convenience wrapper
// gates job execution correctly.
func TestElectorRunOnlyExecutesWhenLeader(t *testing.T) {
	server := newFakeLockServer()
	pool := &fakePool{server: server}
	e := New(pool, "test-job", 99, zap.NewNop())

	var ran bool
	e.Run(context.Background(), func(ctx context.Context) { ran = true })
	if ran {
		t.Fatal("Run must not execute fn before leadership is acquired")
	}

	e.WithInterval(15 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Start(ctx)
	waitFor(t, time.Second, e.IsLeader)

	ran = false
	e.Run(context.Background(), func(ctx context.Context) { ran = true })
	if !ran {
		t.Fatal("Run must execute fn once leadership is held")
	}
}
