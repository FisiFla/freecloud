package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// auditChainDB is a minimal DBPool whose audit-chain query always reports an
// intact chain.
type auditChainDB struct {
	queried bool
}

func (d *auditChainDB) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d *auditChainDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.queried = true
	return &emptyRows{}, nil
}

func (d *auditChainDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	d.queried = true
	if sql == `SELECT ($1::jsonb)::text` {
		return fakeRow{scanFn: func(dest ...any) error {
			*dest[0].(*string) = "{}"
			return nil
		}}
	}
	return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
}

func (d *auditChainDB) Begin(_ context.Context) (pgx.Tx, error) {
	return nil, errors.New("auditChainDB.Begin not implemented")
}

// emptyRows is a pgx.Rows with no rows.
type emptyRows struct{}

func (r *emptyRows) Next() bool                                   { return false }
func (r *emptyRows) Err() error                                   { return nil }
func (r *emptyRows) Close()                                       {}
func (r *emptyRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *emptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *emptyRows) Values() ([]any, error)                       { return nil, nil }
func (r *emptyRows) RawValues() [][]byte                          { return nil }
func (r *emptyRows) Conn() *pgx.Conn                              { return nil }
func (r *emptyRows) Scan(dest ...any) error                       { return pgx.ErrNoRows }

// requestWithRole builds a request carrying the given RBAC role.
func requestWithRole(t *testing.T, role middleware.Role) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/verify", nil)
	ctx := middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:               "test-user",
		PreferredUsername: "test-user",
		Role:              role,
	})
	return req.WithContext(ctx)
}

// TestVerifyAuditChainRequiresSystemAdmin proves the global chain is not
// exposed to an org auditor: the chain spans every tenant, so its integrity
// state and the expected/actual hashes in the error string are cross-tenant
// metadata (issue #133).
func TestVerifyAuditChainRequiresSystemAdmin(t *testing.T) {
	db := &auditChainDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	for _, role := range []middleware.Role{
		middleware.RoleAuditor,
		middleware.RoleHelpdesk,
		middleware.RoleReadOnly,
		middleware.RoleEndUser,
	} {
		rec := httptest.NewRecorder()
		h.VerifyAuditChain(rec, requestWithRole(t, role))
		if rec.Code != http.StatusForbidden {
			t.Errorf("role %s: expected 403, got %d — body: %s", role, rec.Code, rec.Body.String())
		}
		if db.queried {
			t.Errorf("role %s: the chain must not be queried at all", role)
			db.queried = false
		}
	}
}

// TestVerifyAuditChainRejectsAnonymous proves an unauthenticated request is
// rejected fail-closed.
func TestVerifyAuditChainRejectsAnonymous(t *testing.T) {
	db := &auditChainDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/verify", nil)
	rec := httptest.NewRecorder()
	h.VerifyAuditChain(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if db.queried {
		t.Error("the chain must not be queried for an anonymous request")
	}
}

// TestVerifyAuditChainAllowsSystemAdmin proves the endpoint still works for a
// system administrator.
func TestVerifyAuditChainAllowsSystemAdmin(t *testing.T) {
	db := &auditChainDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	rec := httptest.NewRecorder()
	h.VerifyAuditChain(rec, requestWithRole(t, middleware.RoleSuperAdmin))

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for a system admin, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if !db.queried {
		t.Error("expected the chain to be queried for a system admin")
	}
}
