package handlers

// L3 lane issue 11: best-effort audit writes must retry transient failures,
// and on final failure must surface the loss via a Prometheus counter and an
// error-level log entry carrying the action name.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// l3RetryRow is a pgx.Row fake: the chain-head lookup scans into **string
// (left nil = empty chain), the details-canonicalisation lookup into *string.
type l3RetryRow struct{}

func (l3RetryRow) Scan(dest ...any) error {
	for _, d := range dest {
		if s, ok := d.(*string); ok {
			*s = "{}"
		}
	}
	return nil
}

// l3RetryTx is a succeeding pgx.Tx fake; only the methods WriteEntry uses are
// overridden, the rest come from the embedded (nil) interface.
type l3RetryTx struct {
	pgx.Tx
	execs *int32
}

func (tx l3RetryTx) Exec(ctx context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	atomic.AddInt32(tx.execs, 1)
	return pgconn.CommandTag{}, nil
}

func (l3RetryTx) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row { return l3RetryRow{} }
func (l3RetryTx) Commit(ctx context.Context) error                         { return nil }
func (l3RetryTx) Rollback(ctx context.Context) error                       { return nil }

// l3RetryPool fails Begin failBegin times, then hands out a succeeding tx.
type l3RetryPool struct {
	failBegin int
	begins    int32
	execs     int32
}

func (p *l3RetryPool) Exec(ctx context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("l3RetryPool: unexpected Exec")
}

func (p *l3RetryPool) Query(ctx context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("l3RetryPool: unexpected Query")
}

func (p *l3RetryPool) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	return l3RetryRow{}
}

func (p *l3RetryPool) Begin(ctx context.Context) (pgx.Tx, error) {
	if atomic.AddInt32(&p.begins, 1) <= int32(p.failBegin) {
		return nil, errors.New("connection reset by peer")
	}
	return l3RetryTx{execs: &p.execs}, nil
}

func l3RetryHandler(pool *l3RetryPool) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(zap.ErrorLevel)
	return &Handler{db: pool, logger: zap.New(core)}, logs
}

func TestBestEffortRetriesTransientFailure(t *testing.T) {
	pool := &l3RetryPool{failBegin: 2}
	h, _ := l3RetryHandler(pool)

	if err := h.writeAuditEntryBestEffort(context.Background(), "actor", "l3.retry.ok", "t", "id", nil); err != nil {
		t.Fatalf("BestEffort should succeed after transient failures: %v", err)
	}
	if got := atomic.LoadInt32(&pool.begins); got != 3 {
		t.Fatalf("expected 3 write attempts, got %d", got)
	}
	if got := atomic.LoadInt32(&pool.execs); got == 0 {
		t.Fatal("expected the successful attempt to execute statements")
	}
}

// l3AuditFailureCount reads the current value of
// freecloud_audit_write_failures_total for one action label (0 when absent).
// Other tests in this package may increment the same counter family with
// their own actions, so this test asserts a delta, not an absolute value.
func l3AuditFailureCount(t *testing.T, action string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "freecloud_audit_write_failures_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "action" && lp.GetValue() == action {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func TestBestEffortFinalFailureSurfacesCounterAndLog(t *testing.T) {
	pool := &l3RetryPool{failBegin: 100} // persistent outage
	h, logs := l3RetryHandler(pool)

	const action = "l3.retry.final_failure_unique"
	before := l3AuditFailureCount(t, action)
	if err := h.writeAuditEntryBestEffort(context.Background(), "actor", action, "t", "id", nil); err == nil {
		t.Fatal("BestEffort should still return the error on final failure")
	}
	if got := atomic.LoadInt32(&pool.begins); got < 2 {
		t.Fatalf("expected retries on failure, got %d attempt(s)", got)
	}
	if got := l3AuditFailureCount(t, action) - before; got != 1 {
		t.Fatalf("expected failure counter delta 1 for %q, got %v", action, got)
	}

	found := false
	for _, e := range logs.All() {
		if e.Level != zap.ErrorLevel {
			continue
		}
		if e.ContextMap()["action"] == action {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error-level log entry with action field %q", action)
	}
}
