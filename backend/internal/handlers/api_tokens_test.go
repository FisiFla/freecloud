package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func TestCreateAPITokenEmptyBody(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	// name="" → validation error → 400
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAPITokenInvalidRole(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]interface{}{"name": "tok", "role": "root", "serviceIdentity": "ci"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid role: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAPITokenMissingServiceIdentity(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]interface{}{"name": "tok", "role": "auditor"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing serviceIdentity: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAPITokenNilDB(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	b, _ := json.Marshal(map[string]interface{}{"name": "tok", "role": "auditor", "serviceIdentity": "ci"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAPITokenAuditsCreation(t *testing.T) {
	const tokenID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	createdAt := time.Now().UTC()
	auditWritten := false

	tx := &fakeTx{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "INSERT INTO api_tokens") {
				return fakeRow{scanFn: func(dest ...any) error {
					*dest[0].(*string) = tokenID
					*dest[1].(*time.Time) = createdAt
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "INSERT INTO audit_logs") && args[1] == "api_token.create" && args[3] == tokenID {
				auditWritten = true
			}
			return pgconn.CommandTag{}, nil
		},
	}
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) { return tx, nil }}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	b, _ := json.Marshal(map[string]interface{}{"name": "tok", "role": "auditor", "serviceIdentity": "ci"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ActorIDKey, "admin-user")
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.CreateAPIToken(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !auditWritten {
		t.Fatal("expected api_token.create audit record")
	}
}

func TestListAPITokensNilDB(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil)
	rec := httptest.NewRecorder()
	h.ListAPITokens(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeAPITokenAuditsRevocation(t *testing.T) {
	const tokenID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	auditWritten := false

	tx := &fakeTx{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "UPDATE api_tokens") {
				return fakeRow{scanFn: func(dest ...any) error {
					*dest[0].(*string) = "deploy-token"
					*dest[1].(*string) = "auditor"
					*dest[2].(*string) = "ci"
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
		execFn: func(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "INSERT INTO audit_logs") && args[1] == "api_token.revoke" && args[3] == tokenID {
				auditWritten = true
			}
			return pgconn.CommandTag{}, nil
		},
	}
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) { return tx, nil }}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/api-tokens/"+tokenID, nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{tokenID}},
	})
	ctx := context.WithValue(chiCtx, middleware.ActorIDKey, "admin-user")
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.RevokeAPIToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !auditWritten {
		t.Fatal("expected api_token.revoke audit record")
	}
}

func TestRevokeAPITokenInvalidID(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/api-tokens/not-a-uuid", nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{"not-a-uuid"}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.RevokeAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeAPITokenNilDB(t *testing.T) {
	h := setupTestHandler(t) // nil DB
	const validID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/api-tokens/"+validID, nil)
	chiCtx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{validID}},
	})
	req = req.WithContext(chiCtx)
	rec := httptest.NewRecorder()
	h.RevokeAPIToken(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("nil DB: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}
