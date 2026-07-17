package handlers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

func TestJ07_ReviewScheduleRunner_SkipsWhenNotLeader(t *testing.T) {
	// Production: not-leader tick must not query review_schedules.
	var queries atomic.Int32
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			queries.Add(1)
			return nil, errors.New("should not query when not leader")
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			queries.Add(1)
			return pgconn.CommandTag{}, errors.New("should not exec when not leader")
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	h.StartReviewScheduleRunner(ctx, 15*time.Millisecond, func() bool { return false })
	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	if queries.Load() != 0 {
		t.Fatalf("expected no DB access while not leader, got %d", queries.Load())
	}
}
