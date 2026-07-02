package db

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AcquireAdvisoryLock polls pg_try_advisory_lock (non-blocking, single
// round-trip) instead of issuing a blocking `SELECT pg_advisory_lock(...)`.
// This matters because freecloud's backend pool sets a server-side
// statement_timeout: a blocking pg_advisory_lock occupies the connection for
// as long as the lock is contended, and from Postgres's point of view that's
// one long-running statement — the statement_timeout cancels it regardless
// of the caller's context deadline (observed in the B4 HA e2e: a waiting
// replica was killed by "canceling statement due to statement timeout"
// while a sibling replica still legitimately held the lock). A poll loop's
// individual statements are always fast, so only the client-side ctx/timeout
// governs how long the caller is willing to wait.
//
// Returns once the lock is held, ctx is cancelled, or timeout elapses since
// the first attempt (whichever comes first). onWaiting, if non-nil, fires
// once — the first time the lock is found held by someone else — so callers
// can log a "waiting" message without spamming on every poll.
func AcquireAdvisoryLock(ctx context.Context, conn *pgxpool.Conn, lockID int64, timeout time.Duration, onWaiting func()) error {
	deadline := time.Now().Add(timeout)
	announced := false
	for {
		var got bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, lockID).Scan(&got); err != nil {
			return fmt.Errorf("pg_try_advisory_lock(%d): %w", lockID, err)
		}
		if got {
			return nil
		}
		if !announced && onWaiting != nil {
			onWaiting()
			announced = true
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for advisory lock %d", timeout, lockID)
		}
		// ~2s poll interval +/- 20% jitter, matching internal/leader's backoff
		// style, so competing pollers don't all retry in lockstep.
		const interval = 2 * time.Second
		const spread = interval / 5
		jitter := time.Duration(rand.Int63n(int64(spread)))
		wait := interval - spread/2 + jitter
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
