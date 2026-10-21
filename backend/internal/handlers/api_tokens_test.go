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

// TestListAPITokensIsOrgScoped proves the API-token listing is actually
// filtered by the caller's org.
//
// The fake here is a REAL filter (it returns only the rows of the org the
// query bound, and it refuses a query that does not constrain on org_id)
// rather than a synthesiser that invents a row from whatever argument it
// sees. A handler that dropped its `WHERE org_id = $1` clause therefore
// fails the test instead of passing on a fake that echoes the request back
// (issue #228).
func TestListAPITokensIsOrgScoped(t *testing.T) {
	const orgA = "10000000-0000-0000-0000-00000000000a"
	const orgB = "20000000-0000-0000-0000-00000000000b"

	seed := map[string][][]interface{}{
		orgA: {{"token-a", "tok-a", "auditor", "svc-a", time.Now().UTC(), nil}},
		orgB: {{"token-b", "tok-b", "auditor", "svc-b", time.Now().UTC(), nil}},
	}

	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			return orgScopedRows(sql, args, seed)
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	// Org A must see only its own token.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil)
	ctx := middleware.SetOrgContext(req.Context(), &middleware.OrgContext{OrgID: orgA, Role: middleware.OrgMembershipRoleAdmin})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ListAPITokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "tok-a") {
		t.Errorf("expected org A's own token in the response: %s", body)
	}
	if strings.Contains(body, "tok-b") {
		t.Errorf("org A must not see org B's token: %s", body)
	}
}

// TestOrgScopedRowsRejectsUnfilteredQuery proves the org-scoped fake is a
// real filter, not a synthesiser: a query that does not constrain on
// org_id is REJECTED instead of returning a fabricated answer (issue #228).
func TestOrgScopedRowsRejectsUnfilteredQuery(t *testing.T) {
	seed := map[string][][]interface{}{
		"org-a": {{"token-a"}},
	}

	// A query without an org filter must be refused outright.
	if _, err := orgScopedRows("SELECT id FROM api_tokens", []any{"org-a"}, seed); err == nil {
		t.Error("expected an unfiltered query to be rejected")
	}
	// A query that filters on org_id returns only that org's rows.
	rows, err := orgScopedRows("SELECT id FROM api_tokens WHERE org_id = $1", []any{"org-a"}, seed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fr := rows.(*fakeQueryRows)
	if len(fr.rows) != 1 {
		t.Errorf("expected 1 row for org-a, got %d", len(fr.rows))
	}
	// An unknown org yields no rows.
	rows, err = orgScopedRows("SELECT id FROM api_tokens WHERE org_id = $1", []any{"org-b"}, seed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fr := rows.(*fakeQueryRows); len(fr.rows) != 0 {
		t.Errorf("expected no rows for an unknown org, got %d", len(fr.rows))
	}
}

// TestListAPITokensRejectsExcessiveTTL proves a token cannot be created
// with an unbounded lifetime: ExpiresInDays is a client-supplied int, and
// values above the cap are rejected instead of being accepted (issue #123).
func TestCreateAPITokenRejectsExcessiveTTL(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]interface{}{
		"name": "tok", "role": "auditor", "serviceIdentity": "ci",
		"expiresInDays": MaxAPITokenTTLDays + 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for an excessive TTL, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateAPITokenRejectsOverflowingTTL proves a value large enough to
// overflow the old int64 duration arithmetic is rejected rather than
// producing a token that is born expired (issue #123).
func TestCreateAPITokenRejectsOverflowingTTL(t *testing.T) {
	h := setupTestHandler(t)
	b, _ := json.Marshal(map[string]interface{}{
		"name": "tok", "role": "auditor", "serviceIdentity": "ci",
		"expiresInDays": 106751991, // ~292 years: overflows time.Duration
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for an overflowing TTL, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateAPITokenAppliesDefaultTTL proves a non-super-admin token
// created without an explicit expiry still gets a BOUNDED lifetime instead
// of never expiring (issue #123).
func TestCreateAPITokenAppliesDefaultTTL(t *testing.T) {
	var expiresAt *time.Time
	tx := &fakeTx{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
			if strings.Contains(sql, "INSERT INTO api_tokens") {
				// args: name, hash, role, scopes, service_identity, created_by,
				// expires_at, org_id
				if len(args) >= 7 {
					if v, ok := args[6].(*time.Time); ok {
						expiresAt = v
					}
				}
				return fakeRow{scanFn: func(dest ...any) error {
					*dest[0].(*string) = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
					*dest[1].(*time.Time) = time.Now().UTC()
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	db := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) { return tx, nil }}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	b, _ := json.Marshal(map[string]interface{}{"name": "tok", "role": "auditor", "serviceIdentity": "ci", "expiresInDays": 0})
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
	if expiresAt == nil {
		t.Fatal("expected a non-super-admin token to have a bounded expiry")
	}
	got := expiresAt.Sub(time.Now().UTC())
	want := time.Duration(DefaultAPITokenTTLDays) * 24 * time.Hour
	if got < want-time.Hour || got > want+time.Hour {
		t.Errorf("expected an expiry ~%v out, got %v", want, got)
	}
}

func TestCreateAPITokenAuditsCreation(t *testing.T) {
	const tokenID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	createdAt := time.Now().UTC()
	auditWritten := false

	tx := &fakeTx{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
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
			if strings.Contains(sql, "INSERT INTO audit_logs") && args[2] == "api_token.create" && args[4] == tokenID {
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
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if isCanonicalQuery(sql) {
				return fakeCanonicalRow(args)
			}
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
			if strings.Contains(sql, "INSERT INTO audit_logs") && args[2] == "api_token.revoke" && args[4] == tokenID {
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
