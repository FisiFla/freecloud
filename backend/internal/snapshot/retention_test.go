package snapshot

// Regression test for #154: analytics_snapshots grew without bound — one row
// per org per snapshot tick, with no retention sweep.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPrune_RemovesRowsOlderThanTheWindow(t *testing.T) {
	var execSQL string
	var execArgs []any
	pool := &fakePool{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			execSQL = sql
			execArgs = args
			return pgconn.NewCommandTag("DELETE 42"), nil
		},
	}
	s := New(pool, nil)

	deleted, err := s.Prune(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 42 {
		t.Errorf("deleted=%d want 42", deleted)
	}
	if !strings.Contains(execSQL, "DELETE FROM analytics_snapshots") {
		t.Errorf("unexpected SQL: %s", execSQL)
	}
	if !strings.Contains(execSQL, "captured_at <") {
		t.Errorf("the sweep must be bounded by captured_at: %s", execSQL)
	}
	if len(execArgs) != 1 {
		t.Fatalf("expected one cutoff argument, got %v", execArgs)
	}
	cutoff, ok := execArgs[0].(time.Time)
	if !ok {
		t.Fatalf("the cutoff argument must be a time, got %T", execArgs[0])
	}
	if cutoff.After(time.Now()) {
		t.Errorf("the cutoff %v is in the future", cutoff)
	}
}

func TestPrune_ZeroRetentionKeepsEverything(t *testing.T) {
	execCalled := false
	pool := &fakePool{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			execCalled = true
			return pgconn.CommandTag{}, nil
		},
	}
	s := New(pool, nil)

	deleted, err := s.Prune(context.Background(), 0)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 0 || execCalled {
		t.Errorf("a zero retention window must be a no-op (deleted=%d, exec=%t)", deleted, execCalled)
	}
}

func TestPrune_DBErrorIsReported(t *testing.T) {
	pool := &fakePool{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, context.DeadlineExceeded
		},
	}
	s := New(pool, nil)

	if _, err := s.Prune(context.Background(), time.Hour); err == nil {
		t.Fatal("expected the sweep failure to be reported")
	}
}
