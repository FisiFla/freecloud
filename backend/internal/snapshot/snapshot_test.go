package snapshot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap/zaptest"
)

// fakeRow is a pgx.Row whose Scan delegates to a function.
type fakeRow struct {
	scanFn func(dest ...any) error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.scanFn != nil {
		return r.scanFn(dest...)
	}
	return nil
}

// fakeRows backs a Query call with a pre-populated slice of rows.
type fakeRows struct {
	rows [][]any
	idx  int
	err  error
}

func (r *fakeRows) Next() bool          { return r.idx < len(r.rows) }
func (r *fakeRows) Err() error          { return r.err }
func (r *fakeRows) Close()              {}
func (r *fakeRows) CommandTag() pgconn.CommandTag            { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                   { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                      { return nil }
func (r *fakeRows) Conn() *pgx.Conn                          { return nil }
func (r *fakeRows) Scan(dest ...any) error {
	if r.idx > len(r.rows) {
		return errors.New("no more rows")
	}
	row := r.rows[r.idx]
	r.idx++
	for i, d := range dest {
		if i >= len(row) {
			break
		}
		switch p := d.(type) {
		case *int:
			if v, ok := row[i].(int); ok {
				*p = v
			}
		case *int64:
			switch v := row[i].(type) {
			case int64:
				*p = v
			case int:
				*p = int64(v)
			}
		case *float64:
			if v, ok := row[i].(float64); ok {
				*p = v
			}
		case *time.Time:
			if v, ok := row[i].(time.Time); ok {
				*p = v
			}
		}
	}
	return nil
}

// fakePool implements DBPool for unit tests.
type fakePool struct {
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
}

func (p *fakePool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if p.execFn != nil {
		return p.execFn(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, nil
}

func (p *fakePool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if p.queryFn != nil {
		return p.queryFn(ctx, sql, args...)
	}
	return nil, errors.New("fakePool.Query not wired")
}

func (p *fakePool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if p.queryRowFn != nil {
		return p.queryRowFn(ctx, sql, args...)
	}
	return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
}

// queryRowCallSeq returns a fakePool.queryRowFn that cycles through a list of
// responses in order (one per QueryRow call).
func queryRowCallSeq(responses []func(dest ...any) error) func(context.Context, string, ...any) pgx.Row {
	idx := 0
	return func(_ context.Context, _ string, _ ...any) pgx.Row {
		if idx >= len(responses) {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}
		fn := responses[idx]
		idx++
		return fakeRow{scanFn: fn}
	}
}

func TestTakeSnapshot_HappyPath(t *testing.T) {
	execCalled := false

	pool := &fakePool{
		queryRowFn: queryRowCallSeq([]func(dest ...any) error{
			// COUNT(*) FROM devices
			func(dest ...any) error { *(dest[0].(*int)) = 5; return nil },
			// COUNT(*) FROM users (mfaTotal for coverage)
			func(dest ...any) error { *(dest[0].(*int)) = 5; return nil },
			// COUNT(*) FROM mfa_coverage_cache WHERE has_mfa = TRUE (mfaEnrolled)
			func(dest ...any) error { *(dest[0].(*int)) = 3; return nil },
			// COUNT(*) FROM connected_apps
			func(dest ...any) error { *(dest[0].(*int)) = 3; return nil },
			// MAX(captured_at) FROM analytics_snapshots
			func(dest ...any) error { *(dest[0].(*time.Time)) = time.Time{}; return nil },
			// onboard count
			func(dest ...any) error { *(dest[0].(*int)) = 2; return nil },
			// offboard count
			func(dest ...any) error { *(dest[0].(*int)) = 1; return nil },
		}),
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			execCalled = true
			return pgconn.CommandTag{}, nil
		},
	}

	s := New(pool, zaptest.NewLogger(t))
	if err := s.TakeSnapshot(context.Background()); err != nil {
		t.Fatalf("TakeSnapshot returned error: %v", err)
	}
	if !execCalled {
		t.Error("expected INSERT to be called")
	}
}

func TestTakeSnapshot_DBError(t *testing.T) {
	pool := &fakePool{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return errors.New("db down") }}
		},
	}
	s := New(pool, zaptest.NewLogger(t))
	if err := s.TakeSnapshot(context.Background()); err == nil {
		t.Error("expected error when DB returns error")
	}
}

func TestGetSeries_HappyPath(t *testing.T) {
	now := time.Now()
	row1 := []any{int64(2), now, float64(0.9), int(10), float64(0.5), int(4), int(3), int(1)}
	row2 := []any{int64(1), now.Add(-time.Hour), float64(0.8), int(9), float64(0.5), int(4), int(1), int(0)}

	pool := &fakePool{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &fakeRows{rows: [][]any{row1, row2}}, nil
		},
	}

	s := New(pool, zaptest.NewLogger(t))
	series, err := s.GetSeries(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetSeries returned error: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(series))
	}
	// GetSeries reverses to oldest-first; row2 (id=1) should be first.
	if series[0].ID != 1 {
		t.Errorf("expected oldest row first (id=1), got id=%d", series[0].ID)
	}
	if series[1].ComplianceRate != 0.9 {
		t.Errorf("expected compliance_rate=0.9 for newer row, got %f", series[1].ComplianceRate)
	}
}

func TestGetSeries_QueryError(t *testing.T) {
	pool := &fakePool{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return nil, errors.New("query failed")
		},
	}
	s := New(pool, zaptest.NewLogger(t))
	_, err := s.GetSeries(context.Background(), 10)
	if err == nil {
		t.Error("expected error when query fails")
	}
}

func TestGetSeries_LimitClamped(t *testing.T) {
	pool := &fakePool{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			// Verify limit is clamped to 1000
			if len(args) > 0 {
				if v, ok := args[0].(int); ok && v != 1000 {
					return nil, errors.New("limit not clamped")
				}
			}
			return &fakeRows{}, nil
		},
	}
	s := New(pool, zaptest.NewLogger(t))
	_, err := s.GetSeries(context.Background(), 99999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
