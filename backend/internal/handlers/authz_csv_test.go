package handlers

// L3 lane: every CSV writer must sanitize attacker-influenceable cells
// (hostnames, group/campaign names, emails) against formula injection.

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// TestExportAuditLogsCSVSanitizesFormulas proves a malicious actor id from
// the audit trail cannot smuggle a spreadsheet formula into the CSV export.
func TestExportAuditLogsCSVSanitizesFormulas(t *testing.T) {
	entries := []AuditLogEntry{
		{ID: "1", ActorID: "=cmd|'/c calc'!A1", Action: "login",
			TargetType: "user", TargetID: "uid-1", CreatedAt: "2024-01-01T00:00:00Z"},
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
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected header + 1 data row, got %d rows", len(rows))
	}
	if got := rows[1][1]; got != "'=cmd|'/c calc'!A1" {
		t.Errorf("actor cell not sanitized: got %q", got)
	}
}

// TestExportCampaignCSVSanitizesFormulas proves malicious campaign content
// (resource names, emails) cannot smuggle formulas into the review export.
func TestExportCampaignCSVSanitizesFormulas(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*int)) = 1
				return nil
			}}
		},
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &fakeQueryRows{rows: [][]interface{}{
				{"item-1", "camp-1", "+victim@example.com", "user-1",
					"app", "app-1", "@SUM(A1:A100)", "pending", "", nil, nil},
			}}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	ctx := middleware.SetOrgContext(context.Background(), &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
	ctx = context.WithValue(ctx, chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{
			Keys:   []string{"id"},
			Values: []string{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		},
	})
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/campaigns/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/export?format=csv", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ExportCampaign(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected header + 1 data row, got %d rows", len(rows))
	}
	if got := rows[1][2]; got != "'+victim@example.com" {
		t.Errorf("email cell not sanitized: got %q", got)
	}
	if got := rows[1][6]; got != "'@SUM(A1:A100)" {
		t.Errorf("resource-name cell not sanitized: got %q", got)
	}
	if got := rows[1][0]; got != "item-1" {
		t.Errorf("safe cell must pass through unchanged: got %q", got)
	}
}

// TestStreamReportCSVSanitizesFormulas proves the shared report streamer
// (used by the access-review report with campaign names) sanitizes cells.
func TestStreamReportCSVSanitizesFormulas(t *testing.T) {
	rec := httptest.NewRecorder()
	streamReport(rec, "csv", "report.csv",
		[]string{"=1+1", "Quarterly Review"},
		[]string{"campaign_name"},
		func(s string) []string { return []string{s} },
	)

	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 data rows, got %d rows", len(rows))
	}
	if got := rows[1][0]; got != "'=1+1" {
		t.Errorf("formula cell not sanitized: got %q", got)
	}
	if got := rows[2][0]; got != "Quarterly Review" {
		t.Errorf("safe cell must pass through unchanged: got %q", got)
	}
}
