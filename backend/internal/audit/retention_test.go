package audit

import (
	"context"
	"strings"
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
			if strings.Contains(sql, "DELETE FROM audit_logs") {
				return pgconn.NewCommandTag("DELETE 5"), nil
			}
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "WHERE created_at >= $1") {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
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

func TestPruneWritesAnchorForRetainedSuffix(t *testing.T) {
	anchorWritten := false
	db := &fakeDB{
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "DELETE FROM audit_logs") {
				return pgconn.NewCommandTag("DELETE 2"), nil
			}
			if strings.Contains(sql, "INSERT INTO audit_chain_anchors") {
				if args[0] != int64(3) || args[1] != "previous-row-hash" {
					t.Fatalf("unexpected anchor args: %#v", args)
				}
				anchorWritten = true
			}
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "WHERE created_at >= $1") {
				return fakeRow{scanFn: func(dest ...any) error {
					*dest[0].(*int64) = 3
					*dest[1].(*string) = "previous-row-hash"
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error {
				head := "retained-chain-head"
				*dest[0].(**string) = &head
				return nil
			}}
		},
	}
	p := NewPruner(db, zap.NewNop())
	deleted, err := p.Prune(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 rows deleted, got %d", deleted)
	}
	if !anchorWritten {
		t.Fatal("expected prune to write audit chain anchor")
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
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "WHERE created_at >= $1") {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
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
