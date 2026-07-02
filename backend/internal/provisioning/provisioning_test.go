package provisioning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// ---- fake DB ----

type fakeRow struct {
	scanFn func(dest ...any) error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.scanFn != nil {
		return r.scanFn(dest...)
	}
	return pgx.ErrNoRows
}

type fakeRows struct {
	data [][]any
	idx  int
}

func (r *fakeRows) Next() bool                                   { return r.idx < len(r.data) }
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Scan(dest ...any) error {
	if r.idx >= len(r.data) {
		return errors.New("no more rows")
	}
	row := r.data[r.idx]
	r.idx++
	for i, d := range dest {
		if i >= len(row) {
			break
		}
		switch p := d.(type) {
		case *string:
			if v, ok := row[i].(string); ok {
				*p = v
			}
		case *int:
			if v, ok := row[i].(int); ok {
				*p = v
			}
		}
	}
	return nil
}

type fakeDB struct {
	mu         sync.Mutex
	execSQL    []string
	execArgs   [][]any
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	beginFn    func(ctx context.Context) (pgx.Tx, error)
}

func (d *fakeDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execSQL = append(d.execSQL, sql)
	d.execArgs = append(d.execArgs, args)
	return pgconn.CommandTag{}, nil
}

func (d *fakeDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if d.queryFn != nil {
		return d.queryFn(ctx, sql, args...)
	}
	return &fakeRows{}, nil
}

func (d *fakeDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if d.queryRowFn != nil {
		return d.queryRowFn(ctx, sql, args...)
	}
	return fakeRow{}
}

func (d *fakeDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if d.beginFn != nil {
		return d.beginFn(ctx)
	}
	return nil, errors.New("fakeDB.Begin not implemented")
}

// ---- fake connector ----

type mockConnector struct {
	mu             sync.Mutex
	provisionCalls int
	deprovCalls    int
	updateCalls    int
	syncCalls      int
	provisionFn    func(ctx context.Context, user ProvisionableUser) (string, error)
	deprovFn       func(ctx context.Context, remoteID string) error
}

func (m *mockConnector) ProvisionUser(ctx context.Context, user ProvisionableUser) (string, error) {
	m.mu.Lock()
	m.provisionCalls++
	m.mu.Unlock()
	if m.provisionFn != nil {
		return m.provisionFn(ctx, user)
	}
	return "remote-id-1", nil
}

func (m *mockConnector) DeprovisionUser(ctx context.Context, remoteID string) error {
	m.mu.Lock()
	m.deprovCalls++
	m.mu.Unlock()
	if m.deprovFn != nil {
		return m.deprovFn(ctx, remoteID)
	}
	return nil
}

func (m *mockConnector) UpdateUser(ctx context.Context, remoteID string, user ProvisionableUser) error {
	m.mu.Lock()
	m.updateCalls++
	m.mu.Unlock()
	return nil
}

func (m *mockConnector) SyncGroupMembership(ctx context.Context, remoteID string, groups []string) error {
	m.mu.Lock()
	m.syncCalls++
	m.mu.Unlock()
	return nil
}

func (m *mockConnector) Name() string { return "mock" }

// ---- tests ----

func TestEngineProvisionUser_HappyPath(t *testing.T) {
	callCount := 0
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			callCount++
			// First call: upsert (no QueryRow for upsert)
			// Second call: SELECT remote_id, status — return no existing row
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	mc := &mockConnector{}
	eng := NewEngine(db, zap.NewNop())
	eng.RegisterConnector("app-1", mc)

	user := ProvisionableUser{ID: "user-1", Email: "a@b.com", FirstName: "A", LastName: "B"}
	err := eng.ProvisionUser(context.Background(), "app-1", user)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mc.provisionCalls != 1 {
		t.Errorf("expected 1 provision call, got %d", mc.provisionCalls)
	}
	// Should have called Exec for upsert + update-on-success
	db.mu.Lock()
	defer db.mu.Unlock()
	found := false
	for _, sql := range db.execSQL {
		if strings.Contains(sql, "UPDATE provisioning_state") && strings.Contains(sql, "provisioned") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected UPDATE to provisioned status in execSQL: %v", db.execSQL)
	}
}

func TestEngineDeprovisionUser_HappyPath(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = "remote-id-1"
				}
				return nil
			}}
		},
	}
	mc := &mockConnector{}
	eng := NewEngine(db, zap.NewNop())
	eng.RegisterConnector("app-1", mc)

	err := eng.DeprovisionUser(context.Background(), "app-1", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mc.deprovCalls != 1 {
		t.Errorf("expected 1 deprovision call, got %d", mc.deprovCalls)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	found := false
	for _, sql := range db.execSQL {
		if strings.Contains(sql, "deprovisioned") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected UPDATE to deprovisioned in execSQL: %v", db.execSQL)
	}
}

func TestEngineRetryBackoff_PermanentError(t *testing.T) {
	// Simulate an entry that has already failed twice (retryCount=2).
	// One more failure should push it to permanent_error.
	retryCount := 2
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "retry_count") {
				return fakeRow{scanFn: func(dest ...any) error {
					if p, ok := dest[0].(*int); ok {
						*p = retryCount
					}
					return nil
				}}
			}
			// SELECT remote_id, status — no existing row
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	mc := &mockConnector{
		provisionFn: func(ctx context.Context, user ProvisionableUser) (string, error) {
			return "", errors.New("connector down")
		},
	}
	eng := NewEngine(db, zap.NewNop())
	eng.RegisterConnector("app-1", mc)

	user := ProvisionableUser{ID: "user-1", Email: "a@b.com", FirstName: "A", LastName: "B"}
	err := eng.ProvisionUser(context.Background(), "app-1", user)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	found := false
	for _, sql := range db.execSQL {
		if strings.Contains(sql, "permanent_error") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected permanent_error status in execSQL: %v", db.execSQL)
	}
}

func TestEngineNoConnector(t *testing.T) {
	eng := NewEngine(&fakeDB{}, zap.NewNop())
	user := ProvisionableUser{ID: "u1", Email: "a@b.com"}
	err := eng.ProvisionUser(context.Background(), "no-such-app", user)
	if err == nil || !strings.Contains(err.Error(), "no connector") {
		t.Errorf("expected 'no connector' error, got: %v", err)
	}
}

func TestEngineSyncGroupMembership_SkipsIfNotProvisioned(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	mc := &mockConnector{}
	eng := NewEngine(db, zap.NewNop())
	eng.RegisterConnector("app-1", mc)

	err := eng.SyncGroupMembership(context.Background(), "app-1", "user-1", []string{"eng"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mc.syncCalls != 0 {
		t.Errorf("expected 0 sync calls for unprovisioned user, got %d", mc.syncCalls)
	}
}

func TestEngineReconcileAll_PermanentErrorAfterMaxRetries(t *testing.T) {
	appID := "app-reconcile"
	userID := "user-reconcile"
	orgID := "org-reconcile"

	calls := 0
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			calls++
			return &fakeRows{data: [][]any{
				{orgID, appID, userID, "", "error", 2, "a@b.com", "A", "B", ""},
			}}, nil
		},
	}
	mc := &mockConnector{
		provisionFn: func(ctx context.Context, user ProvisionableUser) (string, error) {
			return "", fmt.Errorf("still down")
		},
	}
	eng := NewEngine(db, zap.NewNop())
	eng.RegisterConnector(appID, mc)

	err := eng.ReconcileAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	found := false
	for _, sql := range db.execSQL {
		if strings.Contains(sql, "permanent_error") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected permanent_error after max retries in execSQL: %v", db.execSQL)
	}
}

// TestEngineReconcileAll_MultiOrgProcessesEachOrgsOwnConnector proves
// ReconcileAll processes stale entries from multiple organizations in one
// pass, and that each org's row is provisioned through THAT org's own
// connector (never the other org's) -- the per-org batching added for
// auditability must not accidentally cross-wire connectors between orgs.
func TestEngineReconcileAll_MultiOrgProcessesEachOrgsOwnConnector(t *testing.T) {
	const (
		orgA, appA, userA = "org-a", "app-a", "user-a"
		orgB, appB, userB = "org-b", "app-b", "user-b"
	)

	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			// ORDER BY ps.org_id groups each org's rows together, as the real
			// query does.
			return &fakeRows{data: [][]any{
				{orgA, appA, userA, "", "error", 0, "a@example.com", "A", "One", ""},
				{orgB, appB, userB, "", "error", 0, "b@example.com", "B", "One", ""},
			}}, nil
		},
	}

	mcA := &mockConnector{}
	mcB := &mockConnector{}
	eng := NewEngine(db, zap.NewNop())
	eng.RegisterConnector(appA, mcA)
	eng.RegisterConnector(appB, mcB)

	if err := eng.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mcA.mu.Lock()
	aCalls := mcA.provisionCalls
	mcA.mu.Unlock()
	mcB.mu.Lock()
	bCalls := mcB.provisionCalls
	mcB.mu.Unlock()

	if aCalls != 1 {
		t.Errorf("org A's connector: expected 1 ProvisionUser call, got %d", aCalls)
	}
	if bCalls != 1 {
		t.Errorf("org B's connector: expected 1 ProvisionUser call, got %d", bCalls)
	}
}
