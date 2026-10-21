package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// orgDB is a minimal OrgDB whose membership query returns a fixed set of
// (org_id, role) rows, as Postgres would render them (lower-case uuid text).
type orgDB struct {
	memberships [][2]string
	orgs        map[string]bool
}

func (d *orgDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return fakeOrgRow{}
}

func (d *orgDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &orgRows{rows: d.memberships}, nil
}

// fakeOrgRow scans a single string (the org id) for the orgExists query.
type fakeOrgRow struct{ val string }

func (r fakeOrgRow) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	if p, ok := dest[0].(*string); ok {
		*p = r.val
		return nil
	}
	return nil
}

// orgRows serves the membership rows.
type orgRows struct {
	rows [][2]string
	pos  int
}

func (r *orgRows) Next() bool                                   { return r.pos < len(r.rows) }
func (r *orgRows) Err() error                                   { return nil }
func (r *orgRows) Close()                                       {}
func (r *orgRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *orgRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *orgRows) Values() ([]interface{}, error)               { return nil, nil }
func (r *orgRows) RawValues() [][]byte                          { return nil }
func (r *orgRows) Conn() *pgx.Conn                              { return nil }
func (r *orgRows) Scan(dest ...any) error {
	row := r.rows[r.pos]
	r.pos++
	*dest[0].(*string) = row[0]
	*dest[1].(*string) = row[1]
	return nil
}

// TestOrgContextUppercaseOrgIDHeader proves an upper-case X-Org-Id is
// accepted for a member of that org.
//
// Postgres renders uuid::TEXT in lower case, so the membership map is keyed by
// the lower-case form. The header value is valid hex in either case, so
// without normalisation a member sending an upper-case id was denied with
// "not a member of the requested organization" (issue #140).
func TestOrgContextUppercaseOrgIDHeader(t *testing.T) {
	const orgID = "10000000-0000-0000-0000-0000000000ab"
	db := &orgDB{memberships: [][2]string{{orgID, OrgMembershipRoleAdmin}}}

	var gotOrg, gotRole string
	h := OrgContextMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oc := GetOrgContext(r.Context())
		if oc != nil {
			gotOrg, gotRole = oc.OrgID, oc.Role
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	ctx := SetClaims(req.Context(), &JWTClaims{Sub: "user-1", Role: RoleHelpdesk})
	req = req.WithContext(ctx)
	// Upper-case the hex digits of the org id.
	req.Header.Set("X-Org-Id", "10000000-0000-0000-0000-0000000000AB")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an upper-case X-Org-Id, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotOrg != orgID {
		t.Errorf("expected org %q, got %q", orgID, gotOrg)
	}
	if gotRole != OrgMembershipRoleAdmin {
		t.Errorf("expected role %q, got %q", OrgMembershipRoleAdmin, gotRole)
	}
}

// TestOrgContextLowercaseOrgIDHeader proves the lower-case form still works.
func TestOrgContextLowercaseOrgIDHeader(t *testing.T) {
	const orgID = "10000000-0000-0000-0000-0000000000ab"
	db := &orgDB{memberships: [][2]string{{orgID, OrgMembershipRoleAdmin}}}

	var gotOrg string
	h := OrgContextMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if oc := GetOrgContext(r.Context()); oc != nil {
			gotOrg = oc.OrgID
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	ctx := SetClaims(req.Context(), &JWTClaims{Sub: "user-1", Role: RoleHelpdesk})
	req = req.WithContext(ctx)
	req.Header.Set("X-Org-Id", orgID)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotOrg != orgID {
		t.Errorf("expected org %q, got %q", orgID, gotOrg)
	}
}

// TestOrgContextRejectsForeignOrg proves a non-member still gets 403 — the
// normalisation must not widen access.
func TestOrgContextRejectsForeignOrg(t *testing.T) {
	const orgID = "10000000-0000-0000-0000-0000000000ab"
	db := &orgDB{memberships: [][2]string{{orgID, OrgMembershipRoleAdmin}}}

	h := OrgContextMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	ctx := SetClaims(req.Context(), &JWTClaims{Sub: "user-1", Role: RoleHelpdesk})
	req = req.WithContext(ctx)
	req.Header.Set("X-Org-Id", "20000000-0000-0000-0000-0000000000CD")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for a foreign org, got %d: %s", rec.Code, rec.Body.String())
	}
}
