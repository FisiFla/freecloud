//go:build integration

package leader

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// testPool connects to TEST_DATABASE_URL. Skips when unset (local unit-only runs).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping leader integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func waitLeader(t *testing.T, e *Elector, want bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if e.IsLeader() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("elector %q IsLeader=%v not reached within %s (got %v)", e.job, want, timeout, e.IsLeader())
}

// TestElectorExplicitUnlockAllowsFailoverWithoutProcessDeath proves the
// pg_advisory_unlock path: with a real pgxpool, Release() alone leaves a
// session lock held, so a mid-life releaseIfLeader (failed Ping path) must
// unlock or no peer can ever re-acquire leadership until the backend session dies.
func TestElectorExplicitUnlockAllowsFailoverWithoutProcessDeath(t *testing.T) {
	pool := testPool(t)
	adapter := PoolAdapter{Pool: pool}
	const lockID int64 = 9191919191 // test-only lock id, not used in production

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e1 := New(adapter, "integration-unlock-a", lockID, zap.NewNop()).WithInterval(30 * time.Millisecond)
	e1.Start(ctx)
	waitLeader(t, e1, true, 3*time.Second)

	// Mid-life leadership loss without process death (simulates failed Ping).
	e1.releaseIfLeader()
	if e1.IsLeader() {
		t.Fatal("expected e1 not leader after releaseIfLeader")
	}

	e2 := New(adapter, "integration-unlock-b", lockID, zap.NewNop()).WithInterval(30 * time.Millisecond)
	e2.Start(ctx)
	// If unlock was missing, e2 would never become leader while e1's pooled
	// connection still held the session lock.
	waitLeader(t, e2, true, 3*time.Second)

	cancel()
	// Brief settle so loops exit and release.
	time.Sleep(50 * time.Millisecond)
}

// TestElectorOnlyOneLeaderAtATime races two electors on a real lock.
func TestElectorOnlyOneLeaderAtATime(t *testing.T) {
	pool := testPool(t)
	adapter := PoolAdapter{Pool: pool}
	const lockID int64 = 9191919192

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eA := New(adapter, "integration-race-a", lockID, zap.NewNop()).WithInterval(25 * time.Millisecond)
	eB := New(adapter, "integration-race-b", lockID, zap.NewNop()).WithInterval(25 * time.Millisecond)
	eA.Start(ctx)
	eB.Start(ctx)

	// Wait until exactly one is leader.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, b := eA.IsLeader(), eB.IsLeader()
		if a || b {
			if a && b {
				t.Fatal("both electors claim leadership simultaneously")
			}
			cancel()
			time.Sleep(50 * time.Millisecond)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("neither elector became leader within timeout")
}
