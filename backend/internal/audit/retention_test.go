package audit

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

func TestPruneNoop(t *testing.T) {
	called := false
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			called = true
			return pgconn.CommandTag{}, nil
		},
	}
	p := NewPruner(db, zap.NewNop())
	deleted, err := p.Prune(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 0 || called {
		t.Error("retainFor=0 should be a noop, but Exec was called")
	}
}

func TestPruneDeletesRows(t *testing.T) {
	execCalls := 0
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
			execCalls++
			// First call is the DELETE; simulate 5 rows deleted.
			// Subsequent calls are the audit WriteEntry INSERT (two Exec calls:
			// chainHead QueryRow + INSERT Exec). We only count the DELETE here.
			var tag pgconn.CommandTag
			if execCalls == 1 {
				tag = pgconn.NewCommandTag("DELETE 5")
			}
			return tag, nil
		},
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			// chainHead — no existing rows, return empty hash
			return fakeRow{scanFn: func(dest ...any) error {
				if p, ok := dest[0].(**string); ok {
					*p = nil
				}
				return nil
			}}
		},
	}
	p := NewPruner(db, zap.NewNop())
	deleted, err := p.Prune(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 5 {
		t.Errorf("expected 5 rows deleted, got %d", deleted)
	}
}

func TestPruneWindowBoundary(t *testing.T) {
	// Verify that retainFor > 0 results in an Exec call (the DELETE).
	called := false
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			called = true
			return pgconn.NewCommandTag("DELETE 0"), nil
		},
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if p, ok := dest[0].(**string); ok {
					*p = nil
				}
				return nil
			}}
		},
	}
	p := NewPruner(db, zap.NewNop())
	_, err := p.Prune(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected Exec to be called when retainFor > 0")
	}
}
