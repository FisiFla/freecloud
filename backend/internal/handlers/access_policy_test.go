package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// withAppID injects a chi appId URL param plus a resolved OrgContext (Epic C
// multi-tenant) into a request context, so app-scoped handlers' org-ownership
// guard doesn't fail closed before ever reaching the behavior under test.
func withAppID(req *http.Request, appID string) *http.Request {
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"appId"}, Values: []string{appID}},
	})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
	})
	return req.WithContext(ctx)
}

func TestGetAppPolicyNilDB(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withAppID(httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/policy", nil), "app-1")
	rec := httptest.NewRecorder()
	h.GetAppAccessPolicy(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d", rec.Code)
	}
}

func TestGetAppPolicyNotFound(t *testing.T) {
	db := &fakeDB{
		queryRowFn: ownershipFoundQueryRowFn(func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withAppID(httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/policy", nil), "app-1")
	rec := httptest.NewRecorder()
	h.GetAppAccessPolicy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool            `json:"success"`
		Data    AppAccessPolicy `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if env.Data.RequireEnrolled || env.Data.RequireDiskEncrypted || env.Data.RequireNoCriticalVulns {
		t.Error("expected zero-value policy when no row exists")
	}
}

func TestGetAppPolicyFound(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				// require_enrolled, require_disk_encrypted, require_no_critical_vulns, max_os_age_days, updated_at
				if len(dest) < 5 {
					return nil
				}
				if p, ok := dest[0].(*bool); ok {
					*p = true
				}
				if p, ok := dest[1].(*bool); ok {
					*p = true
				}
				if p, ok := dest[2].(*bool); ok {
					*p = false
				}
				// max_os_age_days — *int, leave nil
				if p, ok := dest[4].(*time.Time); ok {
					*p = ts
				}
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withAppID(httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/policy", nil), "app-1")
	rec := httptest.NewRecorder()
	h.GetAppAccessPolicy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool            `json:"success"`
		Data    AppAccessPolicy `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !env.Data.RequireEnrolled {
		t.Error("expected RequireEnrolled=true")
	}
	if !env.Data.RequireDiskEncrypted {
		t.Error("expected RequireDiskEncrypted=true")
	}
}

func TestUpsertAppPolicyInvalidBody(t *testing.T) {
	db := &fakeDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := withAppID(httptest.NewRequest(http.MethodPut, "/api/v1/apps/app-1/policy", bytes.NewReader([]byte("not-json"))), "app-1")
	rec := httptest.NewRecorder()
	h.UpsertAppAccessPolicy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUpsertAppPolicyRejectsUnsupportedMaxOSAge(t *testing.T) {
	db := &fakeDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	maxAge := 90
	body, _ := json.Marshal(UpsertAppAccessPolicyRequest{RequireDiskEncrypted: true, MaxOsAgeDays: &maxAge})
	req := withAppID(httptest.NewRequest(http.MethodPut, "/api/v1/apps/app-1/policy", bytes.NewReader(body)), "app-1")
	rec := httptest.NewRecorder()
	h.UpsertAppAccessPolicy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertAppPolicyAppNotFound(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(UpsertAppAccessPolicyRequest{RequireDiskEncrypted: true})
	req := withAppID(httptest.NewRequest(http.MethodPut, "/api/v1/apps/app-1/policy", bytes.NewReader(body)), "app-1")
	rec := httptest.NewRecorder()
	h.UpsertAppAccessPolicy(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpsertAppPolicySuccess(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	callN := 0
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			callN++
			if callN == 1 {
				// app existence check — found
				return fakeRow{scanFn: func(dest ...any) error {
					if s, ok := dest[0].(*string); ok {
						*s = "app-1"
					}
					return nil
				}}
			}
			// upsert RETURNING updated_at
			return fakeRow{scanFn: func(dest ...any) error {
				if p, ok := dest[0].(*time.Time); ok {
					*p = ts
				}
				return nil
			}}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(UpsertAppAccessPolicyRequest{RequireDiskEncrypted: true})
	req := withAppID(httptest.NewRequest(http.MethodPut, "/api/v1/apps/app-1/policy", bytes.NewReader(body)), "app-1")
	rec := httptest.NewRecorder()
	h.UpsertAppAccessPolicy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool            `json:"success"`
		Data    AppAccessPolicy `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !env.Data.RequireDiskEncrypted {
		t.Error("expected RequireDiskEncrypted=true in response")
	}
}
