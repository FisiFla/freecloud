package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func withTeamID(r *http.Request, id string) *http.Request {
	chiCtx := context.WithValue(r.Context(), chi.RouteCtxKey, &chi.Context{
		URLParams: chi.RouteParams{Keys: []string{"id"}, Values: []string{id}},
	})
	return r.WithContext(chiCtx)
}

func withOrgAdminCtx(r *http.Request) *http.Request {
	ctx := middleware.SetClaims(r.Context(), &middleware.JWTClaims{Sub: "admin", Role: middleware.RoleSuperAdmin})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin})
	return r.WithContext(ctx)
}

// ---- ListTeams ----

func TestListTeams_Success(t *testing.T) {
	// M1: system-admin claims — ListTeams now restricts non-admins to an
	// empty list (Fleet teams have no per-org concept yet); that
	// restriction is proven separately in org_isolation_test.go.
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil).WithContext(
		middleware.SetClaims(context.Background(), &middleware.JWTClaims{Sub: "admin", Role: middleware.RoleSuperAdmin}))
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool              `json:"success"`
		Data    ListTeamsResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Teams) == 0 {
		t.Error("expected at least one team from fake")
	}
}

func TestListTeams_FleetError(t *testing.T) {
	f := &fakeFleet{
		listTeamsFn: func(ctx context.Context) ([]fleet.Team, error) {
			return nil, fmt.Errorf("fleet down")
		},
	}
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil)
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// ---- CreateTeam ----

func TestCreateTeam_Success(t *testing.T) {
	created := false
	f := &fakeFleet{
		createTeamFn: func(ctx context.Context, name, description string) (*fleet.Team, error) {
			created = true
			return &fleet.Team{ID: 10, Name: name, Description: description}, nil
		},
	}
	// nil DB skips mapping insert path (org context still required).
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(CreateTeamRequest{Name: "Security", Description: "Security team"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !created {
		t.Error("expected CreateTeam to be called")
	}
}

func TestCreateTeam_MissingName(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(CreateTeamRequest{Name: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateTeam_FleetError(t *testing.T) {
	f := &fakeFleet{
		createTeamFn: func(ctx context.Context, name, description string) (*fleet.Team, error) {
			return nil, fmt.Errorf("fleet error")
		},
	}
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(CreateTeamRequest{Name: "Ops"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// ---- AssignTeamPolicy ----

func TestAssignTeamPolicy_Success(t *testing.T) {
	assigned := false
	f := &fakeFleet{
		assignPolicyToTeamFn: func(ctx context.Context, teamID int, policyID string) error {
			assigned = true
			if teamID != 5 || policyID != "pol-001" {
				return fmt.Errorf("unexpected args: teamID=%d policyID=%s", teamID, policyID)
			}
			return nil
		},
	}
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "pol-001"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/5/policies", bytes.NewReader(body))
	req = withTeamID(req, "5")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !assigned {
		t.Error("expected AssignPolicyToTeam to be called")
	}
}

func TestAssignTeamPolicy_InvalidTeamID(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "pol-001"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/not-a-number/policies", bytes.NewReader(body))
	req = withTeamID(req, "not-a-number")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAssignTeamPolicy_MissingPolicyID(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/policies", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestAssignTeamPolicy_FleetError(t *testing.T) {
	f := &fakeFleet{
		assignPolicyToTeamFn: func(ctx context.Context, teamID int, policyID string) error {
			return fmt.Errorf("fleet down")
		},
	}
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "pol-001"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/3/policies", bytes.NewReader(body))
	req = withTeamID(req, "3")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// ---- MoveHostToTeam ----

func TestMoveHostToTeam_Success(t *testing.T) {
	moved := false
	f := &fakeFleet{
		moveHostToTeamFn: func(ctx context.Context, teamID int, hostIDs []string) error {
			moved = true
			if teamID != 2 || len(hostIDs) != 2 {
				return fmt.Errorf("unexpected args")
			}
			return nil
		},
	}
	// H4: MoveHostToTeam now verifies each host belongs to the caller's org
	// before ever calling Fleet — needs an OrgContext and a DB that answers
	// the ownership check.
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{"host-1", "host-2"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/2/hosts", bytes.NewReader(body))
	req = withTeamID(req, "2")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !moved {
		t.Error("expected MoveHostToTeam to be called")
	}
}

func TestMoveHostToTeam_EmptyHostIDs(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/hosts", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMoveHostToTeam_FleetError(t *testing.T) {
	f := &fakeFleet{
		moveHostToTeamFn: func(ctx context.Context, teamID int, hostIDs []string) error {
			return fmt.Errorf("fleet error")
		},
	}
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{"host-a"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/hosts", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// ---- parseIntParam ----

func TestParseIntParam(t *testing.T) {
	var n int
	if _, err := parseIntParam("42", &n); err != nil || n != 42 {
		t.Errorf("expected 42, got %d, err=%v", n, err)
	}
	if _, err := parseIntParam("abc", &n); err == nil {
		t.Error("expected error for non-numeric")
	}
	if _, err := parseIntParam("0", &n); err == nil {
		t.Error("expected error for 0")
	}
}

func TestCreateTeam_RecordsOrgMapping(t *testing.T) {
	const orgID = middleware.DefaultOrgID
	var insertedTeamID int
	var insertedOrg string
	db := &fakeDB{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if len(args) >= 2 {
				if id, ok := args[0].(int); ok {
					insertedTeamID = id
				}
				if o, ok := args[1].(string); ok {
					insertedOrg = o
				}
			}
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		},
	}
	f := &fakeFleet{
		createTeamFn: func(ctx context.Context, name, description string) (*fleet.Team, error) {
			if name != orgID+"/Security" {
				t.Fatalf("expected org-prefixed fleet name, got %q", name)
			}
			return &fleet.Team{ID: 42, Name: name}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(CreateTeamRequest{Name: "Security"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if insertedTeamID != 42 || insertedOrg != orgID {
		t.Fatalf("mapping insert team=%d org=%q", insertedTeamID, insertedOrg)
	}
}

func TestListTeams_NonAdminFiltersByOrgMapping(t *testing.T) {
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			// only team 7 is mapped to caller's org
			return &fakeQueryRows{rows: [][]interface{}{{7}}}, nil
		},
	}
	f := &fakeFleet{
		listTeamsFn: func(ctx context.Context) ([]fleet.Team, error) {
			return []fleet.Team{{ID: 7, Name: "ours"}, {ID: 99, Name: "theirs"}}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil)
	ctx := middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "u1", Role: middleware.RoleHelpdesk})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: middleware.DefaultOrgID, Role: "member"})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data ListTeamsResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Teams) != 1 || resp.Data.Teams[0].ID != 7 {
		t.Fatalf("expected only team 7, got %+v", resp.Data.Teams)
	}
}

func TestCreateTeam_NameTooLong(t *testing.T) {
	h := setupTestHandler(t)
	long := strings.Repeat("a", 121)
	body, _ := json.Marshal(CreateTeamRequest{Name: long})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for long name, got %d", rec.Code)
	}
}

func TestAssignTeamPolicy_ForeignOrgForbidden(t *testing.T) {
	// Non–system-admin with no fleet_team_orgs row → 404 (not leak).
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	f := &fakeFleet{
		assignPolicyToTeamFn: func(ctx context.Context, teamID int, policyID string) error {
			t.Fatal("Fleet AssignPolicyToTeam must not be called for foreign team")
			return nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "pol-x"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/99/policies", bytes.NewReader(body))
	req = withTeamID(req, "99")
	ctx := middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "u1", Role: middleware.RoleHelpdesk})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: middleware.DefaultOrgID, Role: "member"})
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unmapped team, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMoveHostToTeam_ForeignOrgForbidden(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	f := &fakeFleet{
		moveHostToTeamFn: func(ctx context.Context, teamID int, hostIDs []string) error {
			t.Fatal("Fleet MoveHostToTeam must not be called for foreign team")
			return nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{"host-1"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/99/hosts", bytes.NewReader(body))
	req = withTeamID(req, "99")
	ctx := middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "u1", Role: middleware.RoleHelpdesk})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: middleware.DefaultOrgID, Role: "member"})
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unmapped team, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTeams_SystemAdminSeesUnmapped(t *testing.T) {
	// System admin must not be filtered by fleet_team_orgs.
	db := &fakeDB{
		queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			t.Fatal("system admin ListTeams must not query fleet_team_orgs filter path")
			return &fakeQueryRows{}, nil
		},
	}
	f := &fakeFleet{
		listTeamsFn: func(ctx context.Context) ([]fleet.Team, error) {
			return []fleet.Team{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "admin", Role: middleware.RoleSuperAdmin}))
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Data ListTeamsResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Teams) != 2 {
		t.Fatalf("system admin should see all teams, got %d", len(resp.Data.Teams))
	}
}

func TestCreateTeam_NoOrgContext(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(CreateTeamRequest{Name: "X"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// claims without org context
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "admin", Role: middleware.RoleSuperAdmin}))
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without org context, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestValidateFleetTeamDisplayName(t *testing.T) {
	if err := ValidateFleetTeamDisplayName("ok-team"); err != nil {
		t.Fatalf("ok name: %v", err)
	}
	for _, bad := range []string{"a/b", `a\b`, "a..b", "ab\x00c"} {
		if err := ValidateFleetTeamDisplayName(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestCreateTeam_RejectsSlashInName(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(CreateTeamRequest{Name: "evil/prefix"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for slash name, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateTeam_RejectsLongDescription(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(CreateTeamRequest{Name: "ok", Description: strings.Repeat("d", 501)})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for long description, got %d", rec.Code)
	}
}

func TestAssignTeamPolicy_MappedNonAdminAllowed(t *testing.T) {
	// Production path: requireFleetTeamInCallerOrg allows non–system-admin when mapped.
	assigned := false
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	f := &fakeFleet{
		assignPolicyToTeamFn: func(ctx context.Context, teamID int, policyID string) error {
			assigned = true
			return nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "pol-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/7/policies", bytes.NewReader(body))
	req = withTeamID(req, "7")
	ctx := middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "u1", Role: middleware.RoleHelpdesk})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: middleware.DefaultOrgID, Role: "member"})
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mapped helpdesk: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !assigned {
		t.Fatal("expected Fleet assign to run")
	}
}

func TestMoveHostToTeam_MappedNonAdminAllowed(t *testing.T) {
	moved := false
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	f := &fakeFleet{
		moveHostToTeamFn: func(ctx context.Context, teamID int, hostIDs []string) error {
			moved = true
			return nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{"host-1"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/7/hosts", bytes.NewReader(body))
	req = withTeamID(req, "7")
	ctx := middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "u1", Role: middleware.RoleHelpdesk})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: middleware.DefaultOrgID, Role: "member"})
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mapped helpdesk move: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !moved {
		t.Fatal("expected Fleet move")
	}
}

func TestMoveHostToTeam_TrimsEmptyHostIDs(t *testing.T) {
	var got []string
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	f := &fakeFleet{
		moveHostToTeamFn: func(ctx context.Context, teamID int, hostIDs []string) error {
			got = hostIDs
			return nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{"", "  ", "host-ok"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/hosts", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(got) != 1 || got[0] != "host-ok" {
		t.Fatalf("expected cleaned host list [host-ok], got %v", got)
	}
}

func TestMoveHostToTeam_AllEmptyHostIDs(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{"", "  "}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/hosts", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for all-empty hostIds, got %d", rec.Code)
	}
}

func TestCreateTeam_MappingFailureSurfacesError(t *testing.T) {
	// Production: Fleet create succeeds but mapping INSERT fails → 500, not silent 201.
	db := &fakeDB{
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			if strings.Contains(sql, "fleet_team_orgs") {
				return pgconn.CommandTag{}, fmt.Errorf("db down")
			}
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		},
	}
	f := &fakeFleet{
		createTeamFn: func(ctx context.Context, name, description string) (*fleet.Team, error) {
			return &fleet.Team{ID: 9, Name: name}, nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(CreateTeamRequest{Name: "MappedFail"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when mapping fails, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTeams_NonAdminNoOrgForbidden(t *testing.T) {
	f := &fakeFleet{
		listTeamsFn: func(ctx context.Context) ([]fleet.Team, error) {
			return []fleet.Team{{ID: 1, Name: "x"}}, nil
		},
	}
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "u1", Role: middleware.RoleHelpdesk}))
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without org context, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTeams_SortedByID(t *testing.T) {
	f := &fakeFleet{
		listTeamsFn: func(ctx context.Context) ([]fleet.Team, error) {
			return []fleet.Team{{ID: 30, Name: "c"}, {ID: 10, Name: "a"}, {ID: 20, Name: "b"}}, nil
		},
	}
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "admin", Role: middleware.RoleSuperAdmin}))
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data ListTeamsResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Teams) != 3 || resp.Data.Teams[0].ID != 10 || resp.Data.Teams[1].ID != 20 || resp.Data.Teams[2].ID != 30 {
		t.Fatalf("expected sorted IDs 10,20,30 got %+v", resp.Data.Teams)
	}
}

func TestRequireFleetTeam_SystemAdminBypassesMapping(t *testing.T) {
	// System admin must not hit fleet_team_orgs SELECT for ownership.
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "fleet_team_orgs") {
				t.Fatal("system admin must not query fleet_team_orgs for ownership")
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	assigned := false
	f := &fakeFleet{
		assignPolicyToTeamFn: func(ctx context.Context, teamID int, policyID string) error {
			assigned = true
			return nil
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "pol"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/999/policies", bytes.NewReader(body))
	req = withTeamID(req, "999")
	req = withOrgAdminCtx(req) // super-admin
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusOK || !assigned {
		t.Fatalf("system admin bypass: code=%d assigned=%v body=%s", rec.Code, assigned, rec.Body.String())
	}
}
