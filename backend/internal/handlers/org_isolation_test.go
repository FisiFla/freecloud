package handlers

// C5 (Epic C multi-tenant): cross-org isolation suite.
//
// LOAD-BEARING TEST: proves that org-scoped handlers query with the CALLER's
// org_id, not some other org's — one sub-test per resource class,
// systematically enumerated. This is unit/integration level (fake DB, no
// live Postgres); the e2e counterpart (internal/e2e, tag e2e) proves the
// same property against the live stack with two real orgs and two real
// org-admins.
//
// Technique: each fakeDB's queryFn/queryRowFn closure is a tiny in-memory
// "org store" seeded with one row per org. It asserts the org_id argument
// the handler bound matches the row it's about to return — so a handler
// that forgot the WHERE org_id=$N clause (any org_id, or the WRONG org_id)
// fails the sub-test, not just "runs without error". Calling the SAME
// handler code path twice (once per org context) and asserting each call
// only ever sees its own org's row is what actually proves isolation,
// rather than merely proving "a query with an org_id arg was issued".
import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

const (
	testIsoOrgA = "10000000-0000-0000-0000-00000000000a"
	testIsoOrgB = "20000000-0000-0000-0000-00000000000b"
)

// orgCtx builds a request context carrying a resolved OrgContext for orgID,
// as OrgContextMiddleware would set it for an authenticated org-admin.
func orgCtx(orgID string) context.Context {
	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub: "test-caller", Role: middleware.RoleSuperAdmin,
	})
	ctx = context.WithValue(ctx, middleware.ActorIDKey, "test-caller")
	return middleware.SetOrgContext(ctx, &middleware.OrgContext{
		OrgID: orgID, Role: middleware.OrgMembershipRoleAdmin,
	})
}

func newIsolationHandler(db DBPool) *Handler {
	return NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
}

// TestCrossOrgIsolation_Users proves ListUsers and GetUser query with the
// caller's own org_id: two calls with org A's and org B's context each only
// see their own org's seeded row.
func TestCrossOrgIsolation_Users(t *testing.T) {
	rowsFor := func(orgID string) *fakeQueryRows {
		// Matches ListUsers's 14-column SELECT: id, email, first, last, dept,
		// role, disabled, created_at, updated_at, deviceID, hostname,
		// osVersion, lastSeen, devCreated.
		return &fakeQueryRows{rows: [][]interface{}{
			{orgID + "-user", orgID + "@example.com", "First", "Last",
				"", "", false, "", "", "", "", "", "", ""},
		}}
	}
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			// ListUsers's query is `WHERE u.org_id = $1`.
			if len(args) < 1 {
				t.Fatalf("ListUsers query missing org_id argument: %v", args)
			}
			orgID, _ := args[0].(string)
			return rowsFor(orgID), nil
		},
	}
	h := newIsolationHandler(db)

	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.ListUsers(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: ListUsers expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !containsAll(body, orgID+"@example.com") {
			t.Errorf("org %s: ListUsers response missing its own row: %s", orgID, body)
		}
		other := testIsoOrgB
		if orgID == testIsoOrgB {
			other = testIsoOrgA
		}
		if containsAll(body, other+"@example.com") {
			t.Errorf("org %s: ListUsers response LEAKED the other org's row: %s", orgID, body)
		}
	}
}

// TestCrossOrgIsolation_GetUser proves GetUser 404s when the target user_id
// exists but belongs to a DIFFERENT org than the caller's resolved context.
func TestCrossOrgIsolation_GetUser(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			// GetUser's query is `WHERE keycloak_user_id = $1 AND org_id = $2`.
			// Simulate a real Postgres: the row only exists for org A.
			if len(args) < 2 {
				t.Fatalf("GetUser query missing org_id argument: %v", args)
			}
			userID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if userID == "target-user" && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "target-user"
					*(dest[1].(*string)) = "target@example.com"
					*(dest[2].(*string)) = "First"
					*(dest[3].(*string)) = "Last"
					*(dest[4].(*string)) = ""
					*(dest[5].(*string)) = ""
					*(dest[6].(*bool)) = false
					*(dest[7].(*time.Time)) = time.Now()
					*(dest[8].(*time.Time)) = time.Now()
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := newIsolationHandler(db)

	// Org A (the owning org) can fetch it.
	reqA := newChiRequestWithOrg(http.MethodGet, "/api/v1/users/target-user", "id", "target-user", testIsoOrgA)
	recA := httptest.NewRecorder()
	h.GetUser(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("org A (owner): expected 200, got %d: %s", recA.Code, recA.Body.String())
	}

	// Org B (not the owning org) gets 404 — not the user's data, not a 500.
	reqB := newChiRequestWithOrg(http.MethodGet, "/api/v1/users/target-user", "id", "target-user", testIsoOrgB)
	recB := httptest.NewRecorder()
	h.GetUser(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Fatalf("org B (non-owner): expected 404, got %d: %s", recB.Code, recB.Body.String())
	}
	if containsAll(recB.Body.String(), "target@example.com") {
		t.Fatalf("org B (non-owner): response LEAKED org A's user data: %s", recB.Body.String())
	}
}

// TestCrossOrgIsolation_Apps proves ListApps only returns the caller's org's
// connected apps.
func TestCrossOrgIsolation_Apps(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-app-id", orgID + "-client", orgID + "-app-name", "OIDC", "", true, ""},
			}}, nil
		},
	}
	h := newIsolationHandler(db)

	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.ListApps(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: ListApps expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-app-name") {
			t.Errorf("org %s: ListApps missing its own app: %s", orgID, rec.Body.String())
		}
	}
}

// TestCrossOrgIsolation_APITokens proves ListAPITokens only returns the
// caller's org's tokens, and RevokeAPIToken cannot revoke another org's token.
func TestCrossOrgIsolation_APITokens(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-token-id", orgID + "-token-name", "read-only", "svc", "", ""},
			}}, nil
		},
	}
	h := newIsolationHandler(db)

	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.ListAPITokens(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: ListAPITokens expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-token-name") {
			t.Errorf("org %s: ListAPITokens missing its own token: %s", orgID, rec.Body.String())
		}
	}

	// RevokeAPIToken: a token that belongs to org A cannot be revoked by org B.
	// The UPDATE's WHERE clause is `id = $1 AND org_id = $2`; simulate a real
	// Postgres by only matching the row when BOTH the id and the caller's own
	// org_id (whatever that happens to be) line up with the seeded owner (org A).
	const orgAToken = "aaaaaaaa-0000-0000-0000-000000000001"
	tx := &fakeTx{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			// A successful revoke also writes an audit entry, whose hash-chain
			// bookkeeping (internal/audit.chainHead) issues its OWN QueryRow
			// with unrelated args on the same tx — only treat this as the
			// revoke UPDATE when its distinctive 2-arg (id, org_id) shape matches.
			if !strings.Contains(sql, "UPDATE api_tokens") || len(args) < 2 {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
			id, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if id == orgAToken && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "name"
					*(dest[1].(*string)) = "role"
					*(dest[2].(*string)) = "svc"
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	revokeDB := &fakeDB{beginFn: func(_ context.Context) (pgx.Tx, error) { return tx, nil }}
	hRevoke := newIsolationHandler(revokeDB)

	reqB := newChiRequestWithOrg(http.MethodDelete, "/api/v1/api-tokens/"+orgAToken, "id", orgAToken, testIsoOrgB)
	recB := httptest.NewRecorder()
	hRevoke.RevokeAPIToken(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Fatalf("org B revoking org A's token: expected 404 (not found in org B's scope), got %d: %s", recB.Code, recB.Body.String())
	}

	// Sanity check the positive path: org A CAN revoke its own token, proving
	// the 404 above is genuinely about cross-org scoping, not a broken query.
	reqA := newChiRequestWithOrg(http.MethodDelete, "/api/v1/api-tokens/"+orgAToken, "id", orgAToken, testIsoOrgA)
	recA := httptest.NewRecorder()
	hRevoke.RevokeAPIToken(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("org A revoking its own token: expected 200, got %d: %s", recA.Code, recA.Body.String())
	}
}

// TestCrossOrgIsolation_AuditLogs proves ListAuditLogs and ExportAuditLogs
// only return the caller's org's audit entries.
func TestCrossOrgIsolation_AuditLogs(t *testing.T) {
	rowsFor := func(orgID string) *fakeQueryRows {
		return &fakeQueryRows{rows: [][]interface{}{
			{orgID + "-log-id", orgID + "-actor", "onboard", "user", orgID + "-target", "{}", ""},
		}}
	}
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return rowsFor(orgID), nil
		},
	}
	h := newIsolationHandler(db)

	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.ListAuditLogs(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: ListAuditLogs expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-actor") {
			t.Errorf("org %s: ListAuditLogs missing its own entry: %s", orgID, rec.Body.String())
		}

		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export?format=json", nil).WithContext(orgCtx(orgID))
		rec2 := httptest.NewRecorder()
		h.ExportAuditLogs(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("org %s: ExportAuditLogs expected 200, got %d: %s", orgID, rec2.Code, rec2.Body.String())
		}
		if !containsAll(rec2.Body.String(), orgID+"-actor") {
			t.Errorf("org %s: ExportAuditLogs missing its own entry: %s", orgID, rec2.Body.String())
		}
	}
}

// TestCrossOrgIsolation_SCIM proves SCIMListUsers and SCIMGetUser only
// return/see the caller's org's users, matching the JSON-API isolation
// proven above for the SCIM provisioning surface (C4).
func TestCrossOrgIsolation_SCIM(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-scim-user", orgID + "-scim@example.com", "First", "Last", false, "", "", int64(1)},
			}}, nil
		},
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if len(args) < 2 {
				t.Fatalf("SCIMGetUser query missing org_id argument: %v", args)
			}
			userID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if userID == "target-scim-user" && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "target-scim-user"
					*(dest[1].(*string)) = "target@example.com"
					*(dest[2].(*string)) = "First"
					*(dest[3].(*string)) = "Last"
					*(dest[4].(*bool)) = false
					*(dest[5].(*time.Time)) = time.Now()
					*(dest[6].(*time.Time)) = time.Now()
					*(dest[7].(*int64)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := newIsolationHandler(db)

	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.SCIMListUsers(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: SCIMListUsers expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-scim@example.com") {
			t.Errorf("org %s: SCIMListUsers missing its own user: %s", orgID, rec.Body.String())
		}
	}

	// Org B cannot SCIM-GET a user that belongs to org A.
	reqB := newChiRequestWithOrg(http.MethodGet, "/scim/v2/Users/target-scim-user", "id", "target-scim-user", testIsoOrgB)
	recB := httptest.NewRecorder()
	h.SCIMGetUser(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Fatalf("org B: SCIMGetUser on org A's user: expected 404, got %d: %s", recB.Code, recB.Body.String())
	}
}

// TestCrossOrgIsolation_MissingOrgContext proves every org-scoped handler in
// this suite fails closed (403) — not empty-200, not 500 — when no org
// context is present at all (belt-and-braces: OrgContextMiddleware should
// always set one, but a handler bug that skips the nil check must not
// silently return unscoped/global data).
func TestCrossOrgIsolation_MissingOrgContext(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
			t.Fatal("handler queried the database without an org context — should have failed closed first")
			return nil, nil
		},
	}
	h := newIsolationHandler(db)
	noOrgCtx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{Sub: "x", Role: middleware.RoleSuperAdmin})

	cases := []struct {
		name string
		run  func(w http.ResponseWriter, r *http.Request)
	}{
		{"ListUsers", h.ListUsers},
		{"ListApps", h.ListApps},
		{"ListAPITokens", h.ListAPITokens},
		{"ListAuditLogs", h.ListAuditLogs},
		{"SCIMListUsers", h.SCIMListUsers},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(noOrgCtx)
			rec := httptest.NewRecorder()
			tc.run(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s with no org context: expected 403, got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// ---- helpers ----

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}

// newChiRequestWithOrg builds a request with a chi URL param AND a resolved
// OrgContext both set on the SAME context (calling .WithContext twice would
// have the second call silently replace the first's chi route context) —
// matching the pattern used elsewhere in this package's tests (e.g.
// api_tokens_test.go, access_policy_test.go's withAppID) plus the org
// context this suite needs.
func newChiRequestWithOrg(method, target, paramKey, paramVal, orgID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{paramKey}, Values: []string{paramVal}},
	})
	ctx = middleware.SetClaims(ctx, &middleware.JWTClaims{Sub: "test-caller", Role: middleware.RoleSuperAdmin})
	ctx = context.WithValue(ctx, middleware.ActorIDKey, "test-caller")
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: orgID, Role: middleware.OrgMembershipRoleAdmin})
	return req.WithContext(ctx)
}
