package handlers

// L3 lane: authorization regression tests for the per-app access-policy
// write endpoint. Writing an app access policy is a security-policy change
// (it can disable device-posture gates), so it is super-admin only.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// TestRouterHelpdeskCannotUpsertAppPolicy proves a helpdesk caller is
// forbidden from writing an app access policy, even though helpdesk holds
// PermManagePolicies for Fleet team management.
func TestRouterHelpdeskCannotUpsertAppPolicy(t *testing.T) {
	h := setupTestHandler(t)
	r := newRoleTestRouter(h, middleware.RoleHelpdesk)

	body := `{"requireEnrolled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/app-1/policy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("helpdesk PUT /apps/app-1/policy: expected 403, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestRouterSuperAdminCanUpsertAppPolicy proves the super-admin-only gate
// still lets a system admin write the policy (i.e. the gate did not fail
// closed for everyone).
func TestRouterSuperAdminCanUpsertAppPolicy(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "SELECT 1 FROM") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			// INSERT ... RETURNING updated_at
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*time.Time)) = time.Now().UTC()
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newRoleTestRouter(h, middleware.RoleSuperAdmin)

	body := `{"requireEnrolled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/app-1/policy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("super-admin PUT /apps/app-1/policy: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestRouterHelpdeskCanStillPreviewAppPolicy proves the read-only dry-run
// preview stays on PermManagePolicies (reachable by helpdesk): only the
// persisting write moved to super-admin only.
func TestRouterHelpdeskCanStillPreviewAppPolicy(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "SELECT 1 FROM") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newRoleTestRouter(h, middleware.RoleHelpdesk)

	body := `{"clientIp":"10.0.0.1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/policy/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("helpdesk POST /apps/app-1/policy/preview: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
