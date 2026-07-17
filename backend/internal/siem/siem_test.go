//go:build !windows

package siem

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap/zaptest"
)

// fakeRow is a minimal pgx.Row for unit tests.
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

func (r *fakeRows) Next() bool                                   { return r.idx < len(r.rows) }
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Scan(dest ...any) error {
	if r.idx >= len(r.rows) {
		return errors.New("no more rows")
	}
	row := r.rows[r.idx]
	r.idx++
	for i, d := range dest {
		if i >= len(row) {
			break
		}
		switch p := d.(type) {
		case *int64:
			switch v := row[i].(type) {
			case int64:
				*p = v
			case int:
				*p = int64(v)
			}
		case *string:
			if v, ok := row[i].(string); ok {
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

// fakePool implements DBPool for tests.
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
	return &fakeRows{}, nil
}

func (p *fakePool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if p.queryRowFn != nil {
		return p.queryRowFn(ctx, sql, args...)
	}
	return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
}

// fakeSink counts Send calls and optionally returns an error.
type fakeSink struct {
	calls   atomic.Int32
	failErr error
}

func (f *fakeSink) Name() string { return "fake" }
func (f *fakeSink) Send(_ context.Context, _ AuditEntry) error {
	f.calls.Add(1)
	return f.failErr
}

func TestPoll_AdvancesCursor(t *testing.T) {
	now := time.Now()
	execCalled := false
	var advancedTo int64

	pool := &fakePool{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			// cursor read: last_seq = 0
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*int64)) = 0
				return nil
			}}
		},
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &fakeRows{rows: [][]any{
				{int64(1), "uuid-1", "actor1", "onboard", "user", "u1", "{}", now},
				{int64(2), "uuid-2", "actor2", "offboard", "user", "u2", "{}", now},
			}}, nil
		},
		execFn: func(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
			execCalled = true
			if len(args) > 0 {
				if v, ok := args[0].(int64); ok {
					advancedTo = v
				}
			}
			return pgconn.CommandTag{}, nil
		},
	}

	sink := &fakeSink{}
	s := New(pool, sink, zaptest.NewLogger(t))
	s.poll(context.Background())

	if sink.calls.Load() != 2 {
		t.Errorf("expected 2 sink calls, got %d", sink.calls.Load())
	}
	if !execCalled {
		t.Error("expected cursor UPDATE to be called")
	}
	if advancedTo != 2 {
		t.Errorf("expected cursor advanced to 2, got %d", advancedTo)
	}
}

func TestPoll_FailSoft_SinkError(t *testing.T) {
	now := time.Now()
	execCalled := false

	pool := &fakePool{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*int64)) = 0
				return nil
			}}
		},
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &fakeRows{rows: [][]any{
				{int64(1), "uuid-1", "actor1", "onboard", "user", "u1", "{}", now},
			}}, nil
		},
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			execCalled = true
			return pgconn.CommandTag{}, nil
		},
	}

	sink := &fakeSink{failErr: errors.New("sink down")}
	s := New(pool, sink, zaptest.NewLogger(t))
	s.poll(context.Background())

	// Sink failed — cursor must NOT advance.
	if execCalled {
		t.Error("cursor should NOT advance when sink fails")
	}
}

func TestPoll_EmptyBatch(t *testing.T) {
	execCalled := false

	pool := &fakePool{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*int64)) = 5
				return nil
			}}
		},
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			// No rows beyond cursor.
			return &fakeRows{}, nil
		},
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			execCalled = true
			return pgconn.CommandTag{}, nil
		},
	}

	sink := &fakeSink{}
	s := New(pool, sink, zaptest.NewLogger(t))
	s.poll(context.Background())

	if sink.calls.Load() != 0 {
		t.Errorf("expected 0 sink calls for empty batch, got %d", sink.calls.Load())
	}
	if execCalled {
		t.Error("cursor should not be updated on empty batch")
	}
}

func TestHTTPSink_Send(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewHTTPSink(srv.URL, "test-token")
	entry := AuditEntry{
		Seq: 1, ID: "uuid-1", ActorID: "actor1", Action: "onboard",
		TargetType: "user", TargetID: "u1", Details: "{}", CreatedAt: "2024-01-01T00:00:00Z",
	}
	if err := sink.Send(context.Background(), entry); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if len(gotBody) == 0 {
		t.Error("expected non-empty body")
	}
}

func TestHTTPSink_ErrorOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	sink := NewHTTPSink(srv.URL, "")
	err := sink.Send(context.Background(), AuditEntry{})
	if err == nil {
		t.Fatal("expected error on 502, got nil")
	}
}

func TestPoll_CursorReadError(t *testing.T) {
	pool := &fakePool{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return errors.New("cursor read fail") }}
		},
	}
	sink := &fakeSink{}
	s := New(pool, sink, zaptest.NewLogger(t))
	s.poll(context.Background()) // should not panic
	if sink.calls.Load() != 0 {
		t.Error("expected no sink calls on cursor read error")
	}
}

func TestStreamer_LeaderGateSkipsPoll(t *testing.T) {
	// When isLeader returns false, Start must not advance the cursor / send.
	sink := &fakeSink{}
	queryCalled := atomic.Bool{}
	pool := &fakePool{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			queryCalled.Store(true)
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*int64)) = 0
				return nil
			}}
		},
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			queryCalled.Store(true)
			return &fakeRows{}, nil
		},
	}
	s := New(pool, sink, zaptest.NewLogger(t))
	s.SetLeaderGate(func() bool { return false })
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx, 15*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	if queryCalled.Load() {
		t.Fatal("expected no DB poll while not leader")
	}
	if sink.calls.Load() != 0 {
		t.Fatalf("expected 0 sink sends, got %d", sink.calls.Load())
	}
}

func TestStreamer_LeaderGateRunsPollWhenLeader(t *testing.T) {
	sink := &fakeSink{}
	now := time.Now()
	pool := &fakePool{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*int64)) = 0
				return nil
			}}
		},
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &fakeRows{rows: [][]any{
				{int64(1), "id", "a", "act", "t", "tid", "{}", now},
			}}, nil
		},
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
	}
	s := New(pool, sink, zaptest.NewLogger(t))
	s.SetLeaderGate(func() bool { return true })
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx, 15*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	if sink.calls.Load() < 1 {
		t.Fatal("expected at least one sink send while leader")
	}
}

func TestJ06_PollBatchSizeConstant(t *testing.T) {
	if pollBatchSize != 100 {
		t.Fatalf("pollBatchSize=%d want 100", pollBatchSize)
	}
}
