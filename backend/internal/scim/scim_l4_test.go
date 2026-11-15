package scim

// Security-fix lane L4, items 7-8: SCIM PATCH hardening (email-change
// rejection, department preservation, unknown op/path 400s, IdP
// compatibility, no-op version bumps, org-scoped writes) and list-filter
// hardening (unsupported operators rejected, LIKE metacharacters escaped,
// ew support).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/keycloak"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
	gocloak "github.com/Nerzal/gocloak/v13"
)

// ---- fakes ----

type l4Row struct {
	scanFn func(dest ...any) error
}

func (r l4Row) Scan(dest ...any) error { return r.scanFn(dest...) }

type l4Rows struct {
	rows [][]any
	pos  int
}

func (r *l4Rows) Close()                                       {}
func (r *l4Rows) Err() error                                   { return nil }
func (r *l4Rows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *l4Rows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *l4Rows) Next() bool {
	r.pos++
	return r.pos <= len(r.rows)
}
func (r *l4Rows) Scan(dest ...any) error {
	row := r.rows[r.pos-1]
	for i, d := range dest {
		switch t := d.(type) {
		case *string:
			*t = row[i].(string)
		case *bool:
			*t = row[i].(bool)
		case *time.Time:
			*t = row[i].(time.Time)
		case *int64:
			*t = row[i].(int64)
		case *int:
			*t = row[i].(int)
		default:
			panic("l4Rows.Scan: unsupported dest type")
		}
	}
	return nil
}
func (r *l4Rows) Values() ([]any, error) { return r.rows[r.pos-1], nil }
func (r *l4Rows) RawValues() [][]byte    { return nil }
func (r *l4Rows) Conn() *pgx.Conn        { return nil }
func (r *l4Rows) TypeMap() *pgtype.Map   { return nil }

type l4ExecCall struct {
	sql  string
	args []any
}

type l4DB struct {
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	execs      []l4ExecCall
	queries    []l4ExecCall
}

func (d *l4DB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.execs = append(d.execs, l4ExecCall{sql: sql, args: args})
	if d.execFn != nil {
		return d.execFn(ctx, sql, args...)
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (d *l4DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.queries = append(d.queries, l4ExecCall{sql: sql, args: args})
	if d.queryFn != nil {
		return d.queryFn(ctx, sql, args...)
	}
	return &l4Rows{}, nil
}
func (d *l4DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if d.queryRowFn != nil {
		return d.queryRowFn(ctx, sql, args...)
	}
	return l4Row{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
}

type l4KC struct {
	keycloak.KeycloakClientInterface
	updateUserFn      func(ctx context.Context, userID, first, last, dept string, enabled bool) error
	getUserByIDFn     func(ctx context.Context, userID string) (*gocloak.User, error)
	disableUserFn     func(ctx context.Context, userID string) error
	listGroupsByOrgFn func(ctx context.Context, orgID string, first, max int) ([]*gocloak.Group, error)
	updateCalls       []l4UpdateCall
}

type l4UpdateCall struct {
	userID, first, last, dept string
	enabled                   bool
}

func (f *l4KC) UpdateUser(ctx context.Context, userID, first, last, dept string, enabled bool) error {
	f.updateCalls = append(f.updateCalls, l4UpdateCall{userID: userID, first: first, last: last, dept: dept, enabled: enabled})
	if f.updateUserFn != nil {
		return f.updateUserFn(ctx, userID, first, last, dept, enabled)
	}
	return nil
}
func (f *l4KC) GetUserByID(ctx context.Context, userID string) (*gocloak.User, error) {
	if f.getUserByIDFn != nil {
		return f.getUserByIDFn(ctx, userID)
	}
	return &gocloak.User{ID: &userID}, nil
}
func (f *l4KC) DisableUser(ctx context.Context, userID string) error {
	if f.disableUserFn != nil {
		return f.disableUserFn(ctx, userID)
	}
	return nil
}
func (f *l4KC) ListGroupsByOrg(ctx context.Context, orgID string, first, max int) ([]*gocloak.Group, error) {
	if f.listGroupsByOrgFn != nil {
		return f.listGroupsByOrgFn(ctx, orgID, first, max)
	}
	return nil, nil
}

type l4Host struct{}

func (l4Host) PersistOnboard(ctx context.Context, kcUserID string, req OnboardRequest, actorID string, auditDetails any, enrollmentToken string, orgID string) error {
	return nil
}
func (l4Host) WriteAuditEntryBestEffort(ctx context.Context, actorID, action, targetType, targetID string, details any) error {
	return nil
}
func (l4Host) ValidateUserID(id string) error {
	if id == "" {
		return errL4BadID
	}
	return nil
}
func (l4Host) ValidateOpaqueID(id, field string) error {
	if id == "" {
		return errL4BadID
	}
	return nil
}
func (l4Host) IsValidEmail(s string) bool {
	return strings.Contains(s, "@") && strings.Contains(s, ".") && !strings.ContainsAny(s, " \t\n")
}

type l4Err string

func (e l4Err) Error() string { return string(e) }

const errL4BadID = l4Err("bad id")

func l4Service(db *l4DB, kc *l4KC) *Service {
	return NewService(db, kc, zap.NewNop(), l4Host{})
}

// l4UserRowQueryRow returns a queryRowFn serving one user row (version 3)
// for the PATCH SELECT and zeros otherwise.
func l4UserRowQueryRow(email, first, last string, disabled bool) func(ctx context.Context, sql string, args ...any) pgx.Row {
	return func(ctx context.Context, sql string, args ...any) pgx.Row {
		return l4Row{scanFn: func(dest ...any) error {
			if strings.Contains(sql, "FROM users u") {
				*(dest[0].(*string)) = email
				*(dest[1].(*string)) = first
				*(dest[2].(*string)) = last
				*(dest[3].(*bool)) = disabled
				*(dest[4].(*time.Time)) = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				*(dest[5].(*time.Time)) = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
				*(dest[6].(*int64)) = 3
				return nil
			}
			// count queries etc: zero
			if len(dest) == 1 {
				if n, ok := dest[0].(*int); ok {
					*n = 0
					return nil
				}
			}
			return pgx.ErrNoRows
		}}
	}
}

func l4Request(t *testing.T, method, target, userID, orgID, body string) *http.Request {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	rctx := chi.NewRouteContext()
	if userID != "" {
		rctx.URLParams.Add("id", userID)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: orgID})
	return req.WithContext(ctx)
}

func l4DecodeError(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var e ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&e); err != nil {
		t.Fatalf("decode SCIM error body %q: %v", rec.Body.String(), err)
	}
	return e
}

// ---- PATCH: email changes are rejected ----

// Decision (item 7a): REJECTION, not propagation. Propagating userName/email
// to Keycloak would need a new method on KeycloakClientInterface, whose
// other implementation lives outside this lane's files; a SCIM 400 keeps
// the contract in one place and never silently forks the two sides.
func TestL4PatchRejectsUserNameChange(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	kc := &l4KC{}
	s := l4Service(db, kc)
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"userName","value":"bob@example.com"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if e := l4DecodeError(t, rec); e.ScimType != "invalidValue" {
		t.Errorf("scimType = %q, want invalidValue", e.ScimType)
	}
	for _, ex := range db.execs {
		if strings.Contains(ex.sql, "scim_resource_versions") {
			t.Errorf("rejected PATCH must not bump the version, exec: %s", ex.sql)
		}
	}
	if len(kc.updateCalls) != 0 {
		t.Errorf("rejected PATCH must not touch Keycloak, calls: %+v", kc.updateCalls)
	}
}

func TestL4PatchRejectsEmailsChange(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	kc := &l4KC{}
	s := l4Service(db, kc)
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"emails[type eq \"work\"].value","value":"bob@example.com"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestL4PatchRejectsMalformedEmail(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	kc := &l4KC{}
	s := l4Service(db, kc)
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"userName","value":"not-an-email"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed email, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// An unchanged userName (what IdPs send on every sync cycle) is not a
// change and must keep working.
func TestL4PatchUnchangedEmailAccepted(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	kc := &l4KC{}
	s := l4Service(db, kc)
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"userName","value":"alice@example.com"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an unchanged userName, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// ---- PATCH: department is preserved ----

func TestL4PatchPreservesDepartment(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	dept := "Engineering"
	kc := &l4KC{
		getUserByIDFn: func(ctx context.Context, userID string) (*gocloak.User, error) {
			return &gocloak.User{
				ID:         &userID,
				Attributes: &map[string][]string{"department": {dept}},
			}, nil
		},
	}
	s := l4Service(db, kc)
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"name.givenName","value":"Alicia"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(kc.updateCalls) != 1 {
		t.Fatalf("expected 1 Keycloak update, got %+v", kc.updateCalls)
	}
	if kc.updateCalls[0].dept != dept {
		t.Errorf("department passed to Keycloak = %q, want %q (must be preserved, not blanked)", kc.updateCalls[0].dept, dept)
	}
	if kc.updateCalls[0].first != "Alicia" {
		t.Errorf("first name = %q, want Alicia", kc.updateCalls[0].first)
	}
}

// ---- PATCH: unknown op/path and roles ----

func TestL4PatchUnknownOpRejected(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	s := l4Service(db, &l4KC{})
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"move","path":"name.givenName","value":"x"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if e := l4DecodeError(t, rec); e.ScimType != "invalidSyntax" {
		t.Errorf("scimType = %q, want invalidSyntax", e.ScimType)
	}
}

func TestL4PatchUnknownPathRejected(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	s := l4Service(db, &l4KC{})
	for _, path := range []string{"foo.bar", "name.middleName", "phoneNumbers"} {
		body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"` + path + `","value":"x"}]}`
		req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
		rec := httptest.NewRecorder()
		s.PatchUser(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("path %q: expected 400, got %d (%s)", path, rec.Code, rec.Body.String())
			continue
		}
		if e := l4DecodeError(t, rec); e.ScimType != "invalidPath" {
			t.Errorf("path %q: scimType = %q, want invalidPath", path, e.ScimType)
		}
	}
}

func TestL4PatchRolesAndEntitlementsRejected(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	s := l4Service(db, &l4KC{})
	for _, path := range []string{"roles", "entitlements", "groups"} {
		body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"add","path":"` + path + `","value":[{"value":"admin"}]}]}`
		req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
		rec := httptest.NewRecorder()
		s.PatchUser(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("path %q: expected 400, got %d (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// ---- PATCH: real-IdP routine paths keep working ----

func TestL4PatchIdPRoutinePathsWork(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	s := l4Service(db, &l4KC{})
	// active, name.givenName, name.familyName, displayName, externalId and
	// emails[type eq "work"].value (unchanged) — what Entra ID / Okta send
	// on a routine update. displayName/externalId are accepted no-ops
	// (not stored); the rest apply.
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[` +
		`{"op":"replace","path":"active","value":true},` +
		`{"op":"replace","path":"name.givenName","value":"Alicia"},` +
		`{"op":"replace","path":"name.familyName","value":"Alvarez"},` +
		`{"op":"replace","path":"displayName","value":"Alicia Alvarez"},` +
		`{"op":"replace","path":"externalId","value":"ext-123"},` +
		`{"op":"replace","path":"emails[type eq \"work\"].value","value":"alice@example.com"}` +
		`]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for routine IdP paths, got %d (%s)", rec.Code, rec.Body.String())
	}
	var u User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if u.Name.GivenName != "Alicia" || u.Name.FamilyName != "Alvarez" {
		t.Errorf("name not applied: %+v", u.Name)
	}
	if !u.Active {
		t.Error("active=true not applied")
	}
}

func TestL4PatchSchemaQualifiedPathWorks(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	s := l4Service(db, &l4KC{})
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"urn:ietf:params:scim:schemas:core:2.0:User:name.givenName","value":"Alicia"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a schema-qualified path, got %d (%s)", rec.Code, rec.Body.String())
	}
	var u User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if u.Name.GivenName != "Alicia" {
		t.Errorf("schema-qualified path was not applied, givenName = %q", u.Name.GivenName)
	}
}

// ---- PATCH: no-op skips the version bump ----

func TestL4PatchNoOpSkipsVersionBump(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	kc := &l4KC{}
	s := l4Service(db, kc)
	// displayName is a recognised no-op: nothing changes.
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"displayName","value":"Alice A"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	for _, ex := range db.execs {
		t.Errorf("no-op PATCH must not write, exec: %s", ex.sql)
	}
	if len(kc.updateCalls) != 0 {
		t.Errorf("no-op PATCH must not touch Keycloak, calls: %+v", kc.updateCalls)
	}
	var u User
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if u.Meta.Version != `W/"3"` {
		t.Errorf("version = %q, want W/\"3\" (unchanged)", u.Meta.Version)
	}
}

// ---- PATCH/DELETE: writes are org-scoped ----

func TestL4PatchUpdateIsOrgScoped(t *testing.T) {
	db := &l4DB{queryRowFn: l4UserRowQueryRow("alice@example.com", "Alice", "A", false)}
	s := l4Service(db, &l4KC{})
	body := `{"schemas":["` + SchemaPatchOp + `"],"Operations":[{"op":"replace","path":"name.givenName","value":"Alicia"}]}`
	req := l4Request(t, http.MethodPatch, "/scim/v2/Users/u1", "u1", "org1", body)
	rec := httptest.NewRecorder()
	s.PatchUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	found := false
	for _, ex := range db.execs {
		if strings.Contains(ex.sql, "UPDATE users SET") {
			found = true
			if !strings.Contains(ex.sql, "org_id") {
				t.Errorf("PATCH UPDATE lacks the org predicate: %s", ex.sql)
			}
			hasOrg := false
			for _, a := range ex.args {
				if a == "org1" {
					hasOrg = true
				}
			}
			if !hasOrg {
				t.Errorf("PATCH UPDATE lacks the org id arg: %v", ex.args)
			}
		}
	}
	if !found {
		t.Fatal("expected a PATCH UPDATE users exec")
	}
}

func TestL4DeleteUserUpdateIsOrgScoped(t *testing.T) {
	db := &l4DB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return l4Row{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "alice@example.com"
				return nil
			}}
		},
	}
	s := l4Service(db, &l4KC{})
	req := l4Request(t, http.MethodDelete, "/scim/v2/Users/u1", "u1", "org1", "")
	rec := httptest.NewRecorder()
	s.DeleteUser(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	found := false
	for _, ex := range db.execs {
		if strings.Contains(ex.sql, "UPDATE users SET") {
			found = true
			if !strings.Contains(ex.sql, "org_id") {
				t.Errorf("DELETE UPDATE lacks the org predicate: %s", ex.sql)
			}
		}
	}
	if !found {
		t.Fatal("expected a DELETE UPDATE users exec")
	}
}

// ---- list filters ----

func TestL4ListUsersUnsupportedOpRejected(t *testing.T) {
	for _, filter := range []string{
		`userName ne "bob@example.com"`,
		`emails.value pr`,
		`userName pr`,
	} {
		db := &l4DB{}
		s := l4Service(db, &l4KC{})
		target := "/scim/v2/Users?filter=" + url.QueryEscape(filter)
		req := l4Request(t, http.MethodGet, target, "", "org1", "")
		rec := httptest.NewRecorder()
		s.ListUsers(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("filter %q: expected 400, got %d (%s)", filter, rec.Code, rec.Body.String())
			continue
		}
		if e := l4DecodeError(t, rec); e.ScimType != "invalidFilter" {
			t.Errorf("filter %q: scimType = %q, want invalidFilter", filter, e.ScimType)
		}
		if len(db.queries) != 0 {
			t.Errorf("filter %q: rejected filter must not query, queries: %+v", filter, db.queries)
		}
	}
}

func TestL4ListUsersUnsupportedAttrRejected(t *testing.T) {
	db := &l4DB{}
	s := l4Service(db, &l4KC{})
	target := "/scim/v2/Users?filter=" + url.QueryEscape(`displayName eq "Alice"`)
	req := l4Request(t, http.MethodGet, target, "", "org1", "")
	rec := httptest.NewRecorder()
	s.ListUsers(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if e := l4DecodeError(t, rec); e.ScimType != "invalidFilter" {
		t.Errorf("scimType = %q, want invalidFilter", e.ScimType)
	}
}

func TestL4ListUsersLikeMetacharsEscaped(t *testing.T) {
	for _, op := range []string{"co", "sw", "ew"} {
		var countCalls []l4ExecCall
		db := &l4DB{
			queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
				countCalls = append(countCalls, l4ExecCall{sql: sql, args: args})
				return l4Row{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 0
					return nil
				}}
			},
			queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
				return &l4Rows{}, nil
			},
		}
		s := l4Service(db, &l4KC{})
		target := "/scim/v2/Users?filter=" + url.QueryEscape(`userName `+op+` "100%_x"`)
		req := l4Request(t, http.MethodGet, target, "", "org1", "")
		rec := httptest.NewRecorder()
		s.ListUsers(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("op %s: expected 200, got %d (%s)", op, rec.Code, rec.Body.String())
		}
		// Both the data and the count query must escape LIKE metacharacters
		// with an ESCAPE clause — % and _ in the value are literals.
		check := func(kind, sql string, args []any) {
			t.Helper()
			if !strings.Contains(sql, `ESCAPE '\'`) {
				t.Errorf("op %s %s query lacks ESCAPE clause: %s", op, kind, sql)
			}
			if len(args) < 2 {
				t.Fatalf("op %s %s query has no filter arg: %v", op, kind, args)
			}
			pat, _ := args[1].(string)
			if !strings.Contains(pat, `\%`) || !strings.Contains(pat, `\_`) {
				t.Errorf("op %s %s pattern not escaped: %q", op, kind, pat)
			}
		}
		if len(db.queries) != 1 {
			t.Fatalf("op %s: expected 1 data query, got %+v", op, db.queries)
		}
		check("data", db.queries[0].sql, db.queries[0].args)
		if len(countCalls) != 1 {
			t.Fatalf("op %s: expected 1 count query, got %+v", op, countCalls)
		}
		check("count", countCalls[0].sql, countCalls[0].args)
	}
}

func TestL4ListUsersEndsWith(t *testing.T) {
	db := &l4DB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return l4Row{scanFn: func(dest ...any) error {
				*(dest[0].(*int)) = 0
				return nil
			}}
		},
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return &l4Rows{}, nil
		},
	}
	s := l4Service(db, &l4KC{})
	target := "/scim/v2/Users?filter=" + url.QueryEscape(`userName ew "example.com"`)
	req := l4Request(t, http.MethodGet, target, "", "org1", "")
	rec := httptest.NewRecorder()
	s.ListUsers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(db.queries) != 1 {
		t.Fatalf("expected 1 data query, got %+v", db.queries)
	}
	pat, _ := db.queries[0].args[1].(string)
	if pat != "%example.com" {
		t.Errorf("ew pattern = %q, want %%example.com", pat)
	}
}

func TestL4ListGroupsUnsupportedOpRejected(t *testing.T) {
	for _, filter := range []string{
		`displayName ne "Team"`,
		`displayName co "Tea"`,
		`displayName sw "Tea"`,
		`displayName ew "eam"`,
		`displayName pr`,
	} {
		kc := &l4KC{}
		s := l4Service(&l4DB{}, kc)
		target := "/scim/v2/Groups?filter=" + url.QueryEscape(filter)
		req := l4Request(t, http.MethodGet, target, "", "org1", "")
		rec := httptest.NewRecorder()
		s.ListGroups(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("filter %q: expected 400, got %d (%s)", filter, rec.Code, rec.Body.String())
			continue
		}
		if e := l4DecodeError(t, rec); e.ScimType != "invalidFilter" {
			t.Errorf("filter %q: scimType = %q, want invalidFilter", filter, e.ScimType)
		}
	}
}

func TestL4ListGroupsUnsupportedAttrRejected(t *testing.T) {
	s := l4Service(&l4DB{}, &l4KC{})
	target := "/scim/v2/Groups?filter=" + url.QueryEscape(`members eq "u1"`)
	req := l4Request(t, http.MethodGet, target, "", "org1", "")
	rec := httptest.NewRecorder()
	s.ListGroups(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if e := l4DecodeError(t, rec); e.ScimType != "invalidFilter" {
		t.Errorf("scimType = %q, want invalidFilter", e.ScimType)
	}
}

// ---- ew parser ----

func TestL4ParseFilterEndsWith(t *testing.T) {
	node := ParseFilter(`userName ew "example.com"`)
	if node == nil {
		t.Fatal("expected non-nil node for ew")
	}
	if node.Op != OpEw {
		t.Errorf("expected op ew, got %v", node.Op)
	}
	if node.Attr != "username" || node.Value != "example.com" {
		t.Errorf("unexpected attr/value: %q/%q", node.Attr, node.Value)
	}
	if got := node.Op.String(); got != "ew" {
		t.Errorf("Op.String() = %q, want ew", got)
	}
	f, err := ParseFlatFilterStrict(`userName ew "example.com"`)
	if err != nil || f == nil {
		t.Fatalf("flat ew: f=%+v err=%v", f, err)
	}
	if f.Op != "ew" {
		t.Errorf("flat op = %q, want ew", f.Op)
	}
}

func TestL4FilterEvalEndsWith(t *testing.T) {
	node := &FilterNode{Attr: "username", Op: OpEw, Value: "example.com"}
	if !node.Eval(EvalAttrs{"username": "alice@example.com"}) {
		t.Error("expected ew to match a suffix")
	}
	if node.Eval(EvalAttrs{"username": "alice@example.org"}) {
		t.Error("expected ew to reject a non-suffix")
	}
}
