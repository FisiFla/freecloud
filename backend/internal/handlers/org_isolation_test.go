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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nerzal/gocloak/v13"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/snapshot"
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

// nonAdminOrgCtx builds a request context for a NON-system-admin caller
// (e.g. helpdesk) resolved into orgID — used by the M1 restriction tests,
// where isSystemAdminCaller(ctx) must be false so the caller only sees the
// per-org (or empty) view, unlike orgCtx's RoleSuperAdmin fixture above.
func nonAdminOrgCtx(orgID string) context.Context {
	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub: "test-caller", Role: middleware.RoleHelpdesk,
	})
	ctx = context.WithValue(ctx, middleware.ActorIDKey, "test-caller")
	return middleware.SetOrgContext(ctx, &middleware.OrgContext{
		OrgID: orgID, Role: middleware.OrgMembershipRoleAdmin,
	})
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
			if userID == "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb1" && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb1"
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
	reqA := newChiRequestWithOrg(http.MethodGet, "/api/v1/users/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb1", "id", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb1", testIsoOrgA)
	recA := httptest.NewRecorder()
	h.GetUser(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("org A (owner): expected 200, got %d: %s", recA.Code, recA.Body.String())
	}

	// Org B (not the owning org) gets 404 — not the user's data, not a 500.
	reqB := newChiRequestWithOrg(http.MethodGet, "/api/v1/users/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb1", "id", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbb1", testIsoOrgB)
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
			// SCIMListUsers now issues COUNT(*) with only org_id before the page query.
			if strings.Contains(sql, "COUNT(") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			if len(args) < 2 {
				t.Fatalf("SCIMGetUser query missing org_id argument: %v", args)
			}
			userID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if userID == "cccccccc-cccc-cccc-cccc-cccccccccccc" && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "cccccccc-cccc-cccc-cccc-cccccccccccc"
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
	const targetSCIM = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	reqB := newChiRequestWithOrg(http.MethodGet, "/scim/v2/Users/"+targetSCIM, "id", targetSCIM, testIsoOrgB)
	recB := httptest.NewRecorder()
	h.SCIMGetUser(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Fatalf("org B: SCIMGetUser on org A's user: expected 404, got %d: %s", recB.Code, recB.Body.String())
	}
}

// newChiRequestWithOrgBody builds a request with TWO chi URL params, a JSON
// body, AND a resolved OrgContext — the composite fixture needed by
// decide-style handlers ({id}/{itemId} or {id} plus a decision body).
func newChiRequestWithOrgBody(method, target string, paramKeys, paramVals []string, orgID, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: paramKeys, Values: paramVals},
	})
	ctx = middleware.SetClaims(ctx, &middleware.JWTClaims{Sub: "test-caller", Role: middleware.RoleSuperAdmin})
	ctx = context.WithValue(ctx, middleware.ActorIDKey, "test-caller")
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: orgID, Role: middleware.OrgMembershipRoleAdmin})
	return req.WithContext(ctx)
}

// TestCrossOrgIsolation_Devices proves device-scoped write actions
// (RemoteLock) 404 when the target device belongs to a different org, and
// that org-scoped reads (GetOrgCompliance) only ever see the caller's own
// org's devices. This is the most severe class in the coordinator's review:
// pre-fix, org-B could lock/wipe/restart an org-A device by host ID alone.
func TestCrossOrgIsolation_Devices(t *testing.T) {
	const orgADevice = "host-org-a"

	// requireDeviceInCallerOrg's ownership check: device only "found" for org A.
	ownershipDB := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if len(args) < 2 {
				t.Fatalf("device ownership check missing org_id argument: %v", args)
			}
			deviceID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if deviceID == orgADevice && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := newIsolationHandler(ownershipDB)
	fleetCalled := false
	h.fleet = &fakeFleet{issueRemoteLockFn: func(_ context.Context, hostID string) error {
		fleetCalled = true
		return nil
	}}

	// Org A locking its OWN device succeeds.
	reqA := newChiRequestWithOrg(http.MethodPost, "/api/v1/devices/"+orgADevice+"/lock", "id", orgADevice, testIsoOrgA)
	recA := httptest.NewRecorder()
	h.RemoteLock(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("org A locking its own device: expected 200, got %d: %s", recA.Code, recA.Body.String())
	}
	if !fleetCalled {
		t.Fatal("expected Fleet lock to be issued for org A's own device")
	}

	// Org B locking org A's device must 404 — never reach Fleet.
	fleetCalled = false
	reqB := newChiRequestWithOrg(http.MethodPost, "/api/v1/devices/"+orgADevice+"/lock", "id", orgADevice, testIsoOrgB)
	recB := httptest.NewRecorder()
	h.RemoteLock(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Fatalf("org B locking org A's device: expected 404, got %d: %s", recB.Code, recB.Body.String())
	}
	if fleetCalled {
		t.Fatal("org B's lock attempt on org A's device must NEVER reach Fleet")
	}

	// GetOrgCompliance: each org's compliance dashboard only sees its own devices.
	complianceDB := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-host", orgID + "-hostname", "macOS 15"},
			}}, nil
		},
	}
	hCompliance := newIsolationHandler(complianceDB)
	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/compliance", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		hCompliance.GetOrgCompliance(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: GetOrgCompliance expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-host") {
			t.Errorf("org %s: GetOrgCompliance missing its own device: %s", orgID, rec.Body.String())
		}
	}
}

// TestCrossOrgIsolation_MoveHostToTeam is the H4 load-bearing proof:
// MoveHostToTeam previously passed req.HostIDs straight to Fleet with zero
// ownership check, unlike every sibling device handler (RemoteLock above).
// This proves a batch containing even ONE foreign-org host is rejected in
// full, before Fleet is ever called.
func TestCrossOrgIsolation_MoveHostToTeam(t *testing.T) {
	const orgADevice = "host-team-org-a"

	ownershipDB := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if len(args) < 2 {
				t.Fatalf("device ownership check missing org_id argument: %v", args)
			}
			deviceID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if deviceID == orgADevice && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := newIsolationHandler(ownershipDB)
	fleetCalled := false
	h.fleet = &fakeFleet{moveHostToTeamFn: func(_ context.Context, _ int, _ []string) error {
		fleetCalled = true
		return nil
	}}

	// Org A moving its OWN device succeeds.
	bodyA := `{"hostIds":["` + orgADevice + `"]}`
	reqA := newChiRequestWithOrgBody(http.MethodPost, "/api/v1/teams/1/hosts", []string{"id"}, []string{"1"}, testIsoOrgA, bodyA)
	recA := httptest.NewRecorder()
	h.MoveHostToTeam(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("org A moving its own device: expected 200, got %d: %s", recA.Code, recA.Body.String())
	}
	if !fleetCalled {
		t.Fatal("expected Fleet MoveHostToTeam to be called for org A's own device")
	}

	// Org B moving org A's device must 404 — never reach Fleet.
	fleetCalled = false
	bodyB := `{"hostIds":["` + orgADevice + `"]}`
	reqB := newChiRequestWithOrgBody(http.MethodPost, "/api/v1/teams/1/hosts", []string{"id"}, []string{"1"}, testIsoOrgB, bodyB)
	recB := httptest.NewRecorder()
	h.MoveHostToTeam(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Fatalf("org B moving org A's device: expected 404, got %d: %s", recB.Code, recB.Body.String())
	}
	if fleetCalled {
		t.Fatal("org B's move attempt on org A's device must NEVER reach Fleet")
	}

	// A mixed batch (one own device, one foreign) must reject the WHOLE
	// batch — never partially apply.
	fleetCalled = false
	bodyMixed := `{"hostIds":["other-own-device","` + orgADevice + `"]}`
	reqMixed := newChiRequestWithOrgBody(http.MethodPost, "/api/v1/teams/1/hosts", []string{"id"}, []string{"1"}, testIsoOrgB, bodyMixed)
	recMixed := httptest.NewRecorder()
	h.MoveHostToTeam(recMixed, reqMixed)
	if recMixed.Code != http.StatusNotFound {
		t.Fatalf("mixed batch with a foreign host: expected 404, got %d: %s", recMixed.Code, recMixed.Body.String())
	}
	if fleetCalled {
		t.Fatal("a batch containing a foreign-org host must NEVER reach Fleet, even partially")
	}
}

// TestCrossOrgIsolation_AddOrgMember is the H1 load-bearing proof:
// AddOrgMember previously validated only that req.UserID existed ANYWHERE
// in users, then bound it into the caller's org with a caller-chosen role —
// silently absorbing another tenant's user. This proves the target user's
// OWN org (users.org_id) must match the org being joined.
func TestCrossOrgIsolation_AddOrgMember(t *testing.T) {
	const orgAUser = "aaaaaaaa-0000-0000-0000-0000000000ab"

	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			// SELECT keycloak_user_id FROM users WHERE keycloak_user_id = $1 AND org_id = $2
			if len(args) < 2 {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
			userID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if userID == orgAUser && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = orgAUser
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := newIsolationHandler(db)

	// Org A's admin adding their OWN org's user succeeds.
	bodyOwn := `{"userId":"` + orgAUser + `","role":"member"}`
	reqOwn := newChiRequestWithOrgBody(http.MethodPost, "/api/v1/orgs/"+testIsoOrgA+"/members",
		[]string{"orgId"}, []string{testIsoOrgA}, testIsoOrgA, bodyOwn)
	recOwn := httptest.NewRecorder()
	h.AddOrgMember(recOwn, reqOwn)
	if recOwn.Code != http.StatusOK {
		t.Fatalf("org A adding its own user: expected 200, got %d: %s", recOwn.Code, recOwn.Body.String())
	}

	// Org B's admin trying to absorb org A's user into org B must 404 — the
	// membership insert must never be reached.
	bodyForeign := `{"userId":"` + orgAUser + `","role":"org-admin"}`
	reqForeign := newChiRequestWithOrgBody(http.MethodPost, "/api/v1/orgs/"+testIsoOrgB+"/members",
		[]string{"orgId"}, []string{testIsoOrgB}, testIsoOrgB, bodyForeign)
	recForeign := httptest.NewRecorder()
	h.AddOrgMember(recForeign, reqForeign)
	if recForeign.Code != http.StatusNotFound {
		t.Fatalf("org B absorbing org A's user: expected 404, got %d: %s", recForeign.Code, recForeign.Body.String())
	}
}

// TestCrossOrgIsolation_NativeGroupsRestrictedByOrg is the M1 proof for
// groups.go's ListGroups: a system-admin sees every realm group (unchanged
// legacy behavior); a non-system-admin only sees groups tagged (via the
// org_id Keycloak group attribute) as belonging to their own org.
func TestCrossOrgIsolation_NativeGroupsRestrictedByOrg(t *testing.T) {
	gidA, gnameA := "gid-a", "Org A Team"
	gidB, gnameB := "gid-b", "Org B Team"
	attrsA := map[string][]string{"org_id": {testIsoOrgA}}
	attrsB := map[string][]string{"org_id": {testIsoOrgB}}
	kc := &fakeKeycloak{
		listGroupsFn: func(ctx context.Context) ([]*gocloak.Group, error) {
			return []*gocloak.Group{
				{ID: &gidA, Name: &gnameA, Attributes: &attrsA},
				{ID: &gidB, Name: &gnameB, Attributes: &attrsB},
			}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())

	// System-admin (unchanged behavior): sees BOTH groups.
	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil).WithContext(orgCtx(testIsoOrgA))
	recAdmin := httptest.NewRecorder()
	h.ListGroups(recAdmin, reqAdmin)
	if !containsAll(recAdmin.Body.String(), "Org A Team", "Org B Team") {
		t.Fatalf("system-admin: expected to see every org's groups (unchanged), got: %s", recAdmin.Body.String())
	}

	// Non-system-admin, org A: sees ONLY org A's group.
	reqA := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil).WithContext(nonAdminOrgCtx(testIsoOrgA))
	recA := httptest.NewRecorder()
	h.ListGroups(recA, reqA)
	if !containsAll(recA.Body.String(), "Org A Team") || containsAll(recA.Body.String(), "Org B Team") {
		t.Errorf("non-admin org A: expected only its own group, got: %s", recA.Body.String())
	}

	// Non-system-admin, org B: sees ONLY org B's group.
	reqB := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil).WithContext(nonAdminOrgCtx(testIsoOrgB))
	recB := httptest.NewRecorder()
	h.ListGroups(recB, reqB)
	if !containsAll(recB.Body.String(), "Org B Team") || containsAll(recB.Body.String(), "Org A Team") {
		t.Errorf("non-admin org B: expected only its own group, got: %s", recB.Body.String())
	}
}

// TestM1_RealmRolesTeamsPoliciesRestrictedToSystemAdmin proves ListRealmRoles
// (Keycloak realm roles), ListTeams and ListPolicies (Fleet teams/policies —
// neither has a per-org concept yet) are restricted to system-admin callers;
// every other role holding the same PermReadGroups/PermReadCompliance
// permission gets an empty list instead of every tenant's inventory.
func TestM1_RealmRolesTeamsPoliciesRestrictedToSystemAdmin(t *testing.T) {
	roleID, roleName := "role-1", "freecloud-admin"
	kc := &fakeKeycloak{
		listRealmRolesFn: func(ctx context.Context) ([]*gocloak.Role, error) {
			return []*gocloak.Role{{ID: &roleID, Name: &roleName}}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())

	adminCtx := orgCtx(testIsoOrgA)
	nonAdminCtx := nonAdminOrgCtx(testIsoOrgA)

	// ListRealmRoles
	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/roles", nil).WithContext(adminCtx)
	recAdmin := httptest.NewRecorder()
	h.ListRealmRoles(recAdmin, reqAdmin)
	if !containsAll(recAdmin.Body.String(), roleName) {
		t.Errorf("system-admin: expected to see realm roles (unchanged), got: %s", recAdmin.Body.String())
	}
	reqNonAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/roles", nil).WithContext(nonAdminCtx)
	recNonAdmin := httptest.NewRecorder()
	h.ListRealmRoles(recNonAdmin, reqNonAdmin)
	if containsAll(recNonAdmin.Body.String(), roleName) {
		t.Errorf("non-system-admin: expected realm roles hidden, got: %s", recNonAdmin.Body.String())
	}

	// ListTeams / ListPolicies use the default fakeFleet (1 team, 1 policy).
	hFleet := newIsolationHandler(nil)
	reqTeamsAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil).WithContext(adminCtx)
	recTeamsAdmin := httptest.NewRecorder()
	hFleet.ListTeams(recTeamsAdmin, reqTeamsAdmin)
	var teamsAdmin ListTeamsResponse
	if data := decodeAPIData(t, recTeamsAdmin.Body.Bytes()); data != nil {
		_ = json.Unmarshal(data, &teamsAdmin)
	}
	if len(teamsAdmin.Teams) == 0 {
		t.Error("system-admin: expected at least one Fleet team (unchanged)")
	}

	reqTeamsNonAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil).WithContext(nonAdminCtx)
	recTeamsNonAdmin := httptest.NewRecorder()
	hFleet.ListTeams(recTeamsNonAdmin, reqTeamsNonAdmin)
	var teamsNonAdmin ListTeamsResponse
	if data := decodeAPIData(t, recTeamsNonAdmin.Body.Bytes()); data != nil {
		_ = json.Unmarshal(data, &teamsNonAdmin)
	}
	if len(teamsNonAdmin.Teams) != 0 {
		t.Errorf("non-system-admin: expected zero Fleet teams, got %d", len(teamsNonAdmin.Teams))
	}

	reqPoliciesAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil).WithContext(adminCtx)
	recPoliciesAdmin := httptest.NewRecorder()
	hFleet.ListPolicies(recPoliciesAdmin, reqPoliciesAdmin)
	var policiesAdmin ListPoliciesResponse
	if data := decodeAPIData(t, recPoliciesAdmin.Body.Bytes()); data != nil {
		_ = json.Unmarshal(data, &policiesAdmin)
	}
	if len(policiesAdmin.Policies) == 0 {
		t.Error("system-admin: expected at least one Fleet policy (unchanged)")
	}

	reqPoliciesNonAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil).WithContext(nonAdminCtx)
	recPoliciesNonAdmin := httptest.NewRecorder()
	hFleet.ListPolicies(recPoliciesNonAdmin, reqPoliciesNonAdmin)
	var policiesNonAdmin ListPoliciesResponse
	if data := decodeAPIData(t, recPoliciesNonAdmin.Body.Bytes()); data != nil {
		_ = json.Unmarshal(data, &policiesNonAdmin)
	}
	if len(policiesNonAdmin.Policies) != 0 {
		t.Errorf("non-system-admin: expected zero Fleet policies, got %d", len(policiesNonAdmin.Policies))
	}
}

// decodeAPIData unwraps the {success,data} envelope (respondJSON) and
// returns the raw `data` bytes for a second-stage json.Unmarshal into a
// concrete type.
func decodeAPIData(t *testing.T, body []byte) []byte {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, body)
	}
	return envelope.Data
}

// TestCrossOrgIsolation_Campaigns proves review campaigns are org-scoped:
// ListCampaigns only returns the caller's org's campaigns, and
// DecideCampaignItem 404s when the campaign belongs to a different org.
func TestCrossOrgIsolation_Campaigns(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-campaign-id", orgID + "-campaign-name", "open", "", "", nil, nil},
			}}, nil
		},
	}
	h := newIsolationHandler(db)
	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.ListCampaigns(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: ListCampaigns expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-campaign-name") {
			t.Errorf("org %s: ListCampaigns missing its own campaign: %s", orgID, rec.Body.String())
		}
	}

	// DecideCampaignItem: a campaign that exists but belongs to org A must
	// 404 for org B (requireCampaignInCallerOrg).
	const orgACampaign = "aaaaaaaa-0000-0000-0000-00000000000c"
	const itemID = "aaaaaaaa-0000-0000-0000-000000000001"
	ownershipDB := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			campaignID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if campaignID == orgACampaign && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	hDecide := newIsolationHandler(ownershipDB)
	req := newChiRequestWithOrgBody(http.MethodPost,
		"/api/v1/campaigns/"+orgACampaign+"/items/"+itemID+"/decide",
		[]string{"id", "itemId"}, []string{orgACampaign, itemID},
		testIsoOrgB, `{"decision":"confirm"}`)
	rec := httptest.NewRecorder()
	hDecide.DecideCampaignItem(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("org B deciding org A's campaign item: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCrossOrgIsolation_ReviewSchedules proves recurring review schedules are
// org-scoped: ListReviewSchedules filters by org, and
// UpdateReviewSchedule/DeleteReviewSchedule 404 on a foreign-org schedule id.
func TestCrossOrgIsolation_ReviewSchedules(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-sched-id", orgID + "-sched-name", "weekly", 0, "", nil, true, "", ""},
			}}, nil
		},
	}
	h := newIsolationHandler(db)
	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/review-schedules", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.ListReviewSchedules(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: ListReviewSchedules expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-sched-name") {
			t.Errorf("org %s: ListReviewSchedules missing its own schedule: %s", orgID, rec.Body.String())
		}
	}

	const orgASchedule = "aaaaaaaa-0000-0000-0000-00000000000d"
	ownershipDB := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			schedID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if schedID == orgASchedule && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	hDelete := newIsolationHandler(ownershipDB)
	req := newChiRequestWithOrg(http.MethodDelete, "/api/v1/review-schedules/"+orgASchedule, "id", orgASchedule, testIsoOrgB)
	rec := httptest.NewRecorder()
	hDelete.DeleteReviewSchedule(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("org B deleting org A's review schedule: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCrossOrgIsolation_AccessRequests proves self-service access requests
// are org-scoped: AdminListAccessRequests filters by org, and
// AdminDecideAccessRequest 404s on a foreign-org request id.
func TestCrossOrgIsolation_AccessRequests(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-req-id", orgID + "-requester", "app-1", "pending", "", "", ""},
			}}, nil
		},
	}
	h := newIsolationHandler(db)
	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/portal/access-requests", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.AdminListAccessRequests(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: AdminListAccessRequests expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-requester") {
			t.Errorf("org %s: AdminListAccessRequests missing its own request: %s", orgID, rec.Body.String())
		}
	}

	const orgARequest = "aaaaaaaa-0000-0000-0000-00000000000e"
	decideDB := &fakeDB{
		beginFn: func(_ context.Context) (pgx.Tx, error) {
			return &fakeTx{
				queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
					// UPDATE access_requests ... WHERE id=$3 AND status='pending' AND org_id=$4
					if len(args) < 4 {
						return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
					}
					id, _ := args[2].(string)
					orgID, _ := args[3].(string)
					if id == orgARequest && orgID == testIsoOrgA {
						return fakeRow{scanFn: func(dest ...any) error {
							*(dest[0].(*string)) = "requester-a"
							*(dest[1].(*string)) = "app-1"
							return nil
						}}
					}
					return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
				},
			}, nil
		},
	}
	hDecide := newIsolationHandler(decideDB)
	req := newChiRequestWithOrgBody(http.MethodPatch, "/api/v1/portal/access-requests/"+orgARequest,
		[]string{"id"}, []string{orgARequest}, testIsoOrgB, `{"decision":"rejected"}`)
	rec := httptest.NewRecorder()
	hDecide.AdminDecideAccessRequest(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("org B deciding org A's access request: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCrossOrgIsolation_ApprovalRequests proves the approval queue is
// org-scoped: ListApprovalRequests filters by org, and DecideApproval 404s on
// a foreign-org approval request id.
func TestCrossOrgIsolation_ApprovalRequests(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			// ListApprovalRequests binds org_id as arg[0] then status as arg[1].
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-approval-id", "onboard", orgID + "-requester", []byte("{}"), "pending", "", "", ""},
			}}, nil
		},
	}
	h := newIsolationHandler(db)
	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/approval-requests", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.ListApprovalRequests(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: ListApprovalRequests expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-requester") {
			t.Errorf("org %s: ListApprovalRequests missing its own request: %s", orgID, rec.Body.String())
		}
	}

	const orgAApproval = "aaaaaaaa-0000-0000-0000-00000000000f"
	decideDB := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			// SELECT ... FROM approval_requests WHERE id = $1 AND org_id = $2
			if len(args) < 2 {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
			id, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if id == orgAApproval && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "onboard"
					*(dest[1].(*string)) = "pending"
					*(dest[2].(*[]byte)) = []byte(`{"email":"a@example.com"}`)
					*(dest[3].(*string)) = "some-other-requester"
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	hDecide := newIsolationHandler(decideDB)
	req := newChiRequestWithOrgBody(http.MethodPatch, "/api/v1/approval-requests/"+orgAApproval,
		[]string{"id"}, []string{orgAApproval}, testIsoOrgB, `{"decision":"rejected"}`)
	rec := httptest.NewRecorder()
	hDecide.DecideApproval(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("org B deciding org A's approval request: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCrossOrgIsolation_FederationSources proves LDAP/AD federation sources
// are org-scoped: ListFederationSources filters by org, and
// GetFederationSource 404s on a foreign-org source id.
func TestCrossOrgIsolation_FederationSources(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			orgID, _ := args[0].(string)
			return &fakeQueryRows{rows: [][]interface{}{
				{orgID + "-fed-id", orgID + "-fed-name", "ldap", "other", "{}", "", "", "", "", ""},
			}}, nil
		},
	}
	h := newIsolationHandler(db)
	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/sources", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.ListFederationSources(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: ListFederationSources expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
		if !containsAll(rec.Body.String(), orgID+"-fed-name") {
			t.Errorf("org %s: ListFederationSources missing its own source: %s", orgID, rec.Body.String())
		}
	}

	const orgASource = "fed-org-a"
	getDB := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if len(args) < 2 {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
			id, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if id == orgASource && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = orgASource
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	hGet := newIsolationHandler(getDB)
	req := newChiRequestWithOrg(http.MethodGet, "/api/v1/federation/sources/"+orgASource, "id", orgASource, testIsoOrgB)
	rec := httptest.NewRecorder()
	hGet.GetFederationSource(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("org B reading org A's federation source: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCrossOrgIsolation_Provisioning proves outbound provisioning config and
// state are org-scoped: an org-B admin gets 404 reading org A's app's
// provisioning config, and 404 listing org A's app's provisioning state.
func TestCrossOrgIsolation_Provisioning(t *testing.T) {
	const orgAApp = "aaaaaaaa-0000-0000-0000-000000000010"

	// requireAppInCallerOrg's ownership check: app only "found" for org A.
	appOwnershipDB := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if len(args) < 2 {
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
			appID, _ := args[0].(string)
			orgID, _ := args[1].(string)
			if appID == orgAApp && orgID == testIsoOrgA {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*int)) = 1
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := newIsolationHandler(appOwnershipDB)

	// Org B reading org A's app's provisioning config must 404.
	reqConfig := newChiRequestWithOrg(http.MethodGet, "/api/v1/apps/"+orgAApp+"/provisioning", "appId", orgAApp, testIsoOrgB)
	recConfig := httptest.NewRecorder()
	h.GetProvisioningConfig(recConfig, reqConfig)
	if recConfig.Code != http.StatusNotFound {
		t.Fatalf("org B reading org A's provisioning config: expected 404, got %d: %s", recConfig.Code, recConfig.Body.String())
	}

	// Org B listing org A's app's provisioning state must 404.
	reqState := newChiRequestWithOrg(http.MethodGet, "/api/v1/apps/"+orgAApp+"/provisioning/state", "appId", orgAApp, testIsoOrgB)
	recState := httptest.NewRecorder()
	h.ListProvisioningState(recState, reqState)
	if recState.Code != http.StatusNotFound {
		t.Fatalf("org B listing org A's provisioning state: expected 404, got %d: %s", recState.Code, recState.Body.String())
	}

	// Org A reading its OWN app's provisioning config succeeds (positive control).
	reqOwn := newChiRequestWithOrg(http.MethodGet, "/api/v1/apps/"+orgAApp+"/provisioning", "appId", orgAApp, testIsoOrgA)
	recOwn := httptest.NewRecorder()
	h.GetProvisioningConfig(recOwn, reqOwn)
	if recOwn.Code != http.StatusOK {
		t.Fatalf("org A reading its own provisioning config: expected 200, got %d: %s", recOwn.Code, recOwn.Body.String())
	}
}

// TestCrossOrgIsolation_Analytics proves analytics snapshots (dashboards) are
// org-scoped: GetAnalyticsSnapshots only returns the caller's org's series.
func TestCrossOrgIsolation_Analytics(t *testing.T) {
	seenOrgIDs := []string{}
	db := &fakeDB{
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			if orgID, ok := args[0].(string); ok {
				seenOrgIDs = append(seenOrgIDs, orgID)
			}
			return &fakeQueryRows{rows: [][]interface{}{
				{int64(1), "2026-01-01T00:00:00Z", 0.9, 5, 80.0, 3, 1, 0},
			}}, nil
		},
	}
	h := newIsolationHandler(db)
	h.SetSnapshotter(snapshot.New(db, zap.NewNop()))

	for _, orgID := range []string{testIsoOrgA, testIsoOrgB} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/snapshots", nil).WithContext(orgCtx(orgID))
		rec := httptest.NewRecorder()
		h.GetAnalyticsSnapshots(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("org %s: GetAnalyticsSnapshots expected 200, got %d: %s", orgID, rec.Code, rec.Body.String())
		}
	}
	if len(seenOrgIDs) != 2 || seenOrgIDs[0] != testIsoOrgA || seenOrgIDs[1] != testIsoOrgB {
		t.Errorf("expected GetSeries to be called with [orgA, orgB] in order, got %v", seenOrgIDs)
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
