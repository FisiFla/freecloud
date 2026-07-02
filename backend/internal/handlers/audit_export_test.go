package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// withDefaultOrg attaches a resolved org context to a test request. These
// tests call handlers directly (bypassing SetupRoutes / OrgContextMiddleware),
// so org resolution must be injected manually — mirrors what the real
// middleware chain would have set for an org-admin of the Default Organization.
func withDefaultOrg(req *http.Request) *http.Request {
	ctx := middleware.SetOrgContext(req.Context(), &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
	return req.WithContext(ctx)
}

// fakeRows implements pgx.Rows for ExportAuditLogs tests.
type fakeAuditRows struct {
	entries []AuditLogEntry
	idx     int
	closed  bool
}

func (r *fakeAuditRows) Close()                                               { r.closed = true }
func (r *fakeAuditRows) Err() error                                           { return nil }
func (r *fakeAuditRows) CommandTag() pgconn.CommandTag                         { return pgconn.CommandTag{} }
func (r *fakeAuditRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeAuditRows) Next() bool {
	r.idx++
	return r.idx <= len(r.entries)
}
func (r *fakeAuditRows) Scan(dest ...any) error {
	e := r.entries[r.idx-1]
	detailsB, _ := json.Marshal(e.Details)
	ts, _ := time.Parse(time.RFC3339, e.CreatedAt)
	if ts.IsZero() {
		ts = time.Now()
	}
	// Expected dest order: id, actor_id, action, target_type, target_id, details, created_at
	if len(dest) < 7 {
		return nil
	}
	*dest[0].(*string) = e.ID
	*dest[1].(*string) = e.ActorID
	*dest[2].(*string) = e.Action
	*dest[3].(*string) = e.TargetType
	*dest[4].(*string) = e.TargetID
	*dest[5].(*[]byte) = detailsB
	*dest[6].(*time.Time) = ts
	return nil
}
func (r *fakeAuditRows) Values() ([]any, error)    { return nil, nil }
func (r *fakeAuditRows) RawValues() [][]byte        { return nil }
func (r *fakeAuditRows) Conn() *pgx.Conn            { return nil }

// fakeDBWithQuery extends fakeDB to support Query.
type fakeDBWithQuery struct {
	fakeDB
	queryFn func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (d *fakeDBWithQuery) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if d.queryFn != nil {
		return d.queryFn(ctx, sql, args...)
	}
	return &fakeAuditRows{}, nil
}

// TestExportAuditLogsCSV: basic CSV export returns correct headers and rows.
func TestExportAuditLogsCSV(t *testing.T) {
	entries := []AuditLogEntry{
		{ID: "1", ActorID: "actor1", Action: "onboard",
			TargetType: "user", TargetID: "uid-1",
			Details:   map[string]interface{}{"email": "a@b.com"},
			CreatedAt: "2024-01-01T00:00:00Z"},
	}
	db := &fakeDBWithQuery{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeAuditRows{entries: entries}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := withDefaultOrg(httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export?format=csv", nil))
	rec := httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/csv") {
		t.Errorf("expected text/csv content type, got %q", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("expected attachment disposition, got %q", cd)
	}
	r := csv.NewReader(rec.Body)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected header + 1 data row, got %d rows", len(rows))
	}
	// Header row check
	header := rows[0]
	if header[0] != "id" || header[2] != "action" {
		t.Errorf("unexpected CSV header: %v", header)
	}
	// Data row check
	if rows[1][2] != "onboard" {
		t.Errorf("expected action onboard in row, got %q", rows[1][2])
	}
}

// TestExportAuditLogsJSON: JSON export returns array wrapped in envelope.
func TestExportAuditLogsJSON(t *testing.T) {
	entries := []AuditLogEntry{
		{ID: "2", ActorID: "actor2", Action: "app_create",
			Details: map[string]interface{}{}, CreatedAt: "2024-01-01T00:00:00Z"},
	}
	db := &fakeDBWithQuery{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeAuditRows{entries: entries}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := withDefaultOrg(httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export?format=json", nil))
	rec := httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json, got %q", ct)
	}
	// The JSON export is a raw array (no envelope), so parse directly.
	var out []AuditLogEntry
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1 entry, got %d", len(out))
	}
}

// TestExportAuditLogsInvalidFormat: unknown format → 400.
func TestExportAuditLogsInvalidFormat(t *testing.T) {
	db := &fakeDBWithQuery{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export?format=xml", nil)
	rec := httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// TestExportAuditLogsDefaultFormat: no format param → defaults to CSV.
func TestExportAuditLogsDefaultFormat(t *testing.T) {
	db := &fakeDBWithQuery{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeAuditRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withDefaultOrg(httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export", nil))
	rec := httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/csv") {
		t.Errorf("expected csv default, got %q", ct)
	}
}

// TestExportAuditLogsNoDB → 500.
func TestExportAuditLogsNoDB(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export", nil)
	rec := httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 with no DB, got %d", rec.Code)
	}
}

// TestExportAuditLogsDateRangeInvalid: bad from/to → 400.
func TestExportAuditLogsDateRangeInvalid(t *testing.T) {
	db := &fakeDBWithQuery{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeAuditRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export?from=not-a-date", nil)
	rec := httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad from: expected 400, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export?to=not-a-date", nil)
	rec = httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad to: expected 400, got %d", rec.Code)
	}
}

// TestExportAuditLogsDateRangePassedToQuery: valid from/to are appended to SQL args.
func TestExportAuditLogsDateRangePassedToQuery(t *testing.T) {
	var capturedArgs []any
	db := &fakeDBWithQuery{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			capturedArgs = args
			return &fakeAuditRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := withDefaultOrg(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit-logs/export?format=csv&from=2024-01-01T00:00:00Z&to=2024-12-31T23:59:59Z", nil))
	rec := httptest.NewRecorder()
	h.ExportAuditLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// org_id is always arg 0; the two time args follow (no actor/action → positions 1 and 2).
	if len(capturedArgs) < 3 {
		t.Fatalf("expected at least 3 SQL args (org_id, from, to), got %d", len(capturedArgs))
	}
}

// TestListAuditLogsDateRangeInvalid: bad from/to → 400.
func TestListAuditLogsDateRangeInvalid(t *testing.T) {
	db := &fakeDBWithQuery{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeAuditRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?from=baddate", nil)
	rec := httptest.NewRecorder()
	h.ListAuditLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad from: expected 400, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?to=baddate", nil)
	rec = httptest.NewRecorder()
	h.ListAuditLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad to: expected 400, got %d", rec.Code)
	}
}

// TestListAuditLogsDateRangePassedToQuery: valid from/to appear in SQL args.
func TestListAuditLogsDateRangePassedToQuery(t *testing.T) {
	var capturedArgs []any
	db := &fakeDBWithQuery{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			capturedArgs = args
			return &fakeAuditRows{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := withDefaultOrg(httptest.NewRequest(http.MethodGet,
		"/api/v1/audit-logs?from=2024-01-01T00:00:00Z&to=2024-06-30T00:00:00Z", nil))
	rec := httptest.NewRecorder()
	h.ListAuditLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// org_id is always arg 0; no actor/action → args are: org_id, from, to, limit, offset (5 args).
	if len(capturedArgs) < 5 {
		t.Fatalf("expected 5 SQL args (org_id, from, to, limit, offset), got %d", len(capturedArgs))
	}
}
