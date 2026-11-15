package handlers

// L3 lane: a super-admin-role API token is confined to the org it was
// created in. It must get its own org's view (or 403/404) wherever a human
// system admin would get the cross-tenant view.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nerzal/gocloak/v13"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// apiTokenCtx builds the context handleAPIToken would produce for a
// super-admin-role token created in orgID: super-admin Role, IsAPIToken set,
// OrgContext pinned to the token's own org.
func apiTokenCtx(orgID string) context.Context {
	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub:               "api-token:00000000-0000-0000-0000-000000000001",
		PreferredUsername: "api-token:ci",
		Role:              middleware.RoleSuperAdmin,
		IsAdmin:           true,
		IsAPIToken:        true,
	})
	ctx = context.WithValue(ctx, middleware.ActorIDKey, "api-token:ci")
	return middleware.SetOrgContext(ctx, &middleware.OrgContext{
		OrgID: orgID, Role: middleware.OrgMembershipRoleAdmin,
	})
}

// humanAdminCtx is the same org context for a HUMAN system admin (JWT).
func humanAdminCtx(orgID string) context.Context {
	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub:               "human-admin",
		PreferredUsername: "human-admin",
		Role:              middleware.RoleSuperAdmin,
		IsAdmin:           true,
	})
	ctx = context.WithValue(ctx, middleware.ActorIDKey, "human-admin")
	return middleware.SetOrgContext(ctx, &middleware.OrgContext{
		OrgID: orgID, Role: middleware.OrgMembershipRoleAdmin,
	})
}

func TestAPITokenVerifyAuditChainForbidden(t *testing.T) {
	db := &auditChainDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/verify", nil).WithContext(apiTokenCtx(testIsoOrgA))
	rec := httptest.NewRecorder()
	h.VerifyAuditChain(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("super-admin token: expected 403 on global chain verify, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if db.queried {
		t.Error("the chain must not be queried for an API token")
	}
}

func TestAPITokenGetAuditIntegrityForbidden(t *testing.T) {
	db := &auditChainDB{}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/integrity", nil).WithContext(apiTokenCtx(testIsoOrgA))
	rec := httptest.NewRecorder()
	h.GetAuditIntegrity(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("super-admin token: expected 403 on global chain integrity, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if db.queried {
		t.Error("the chain must not be queried for an API token")
	}
}

// teamOrgDB answers the fleet_team_orgs mapping query: team 1 belongs to
// org A, team 2 belongs to org B.
func teamOrgDB() DBPool {
	return &fakeDB{
		queryRowFn: func(_ context.Context, sql string, args ...any) pgx.Row {
			if strings.Contains(sql, "FROM fleet_team_orgs") && strings.Contains(sql, "fleet_team_id = $1") {
				teamID, _ := args[0].(int)
				orgID, _ := args[1].(string)
				owner := map[int]string{1: testIsoOrgA, 2: testIsoOrgB}[teamID]
				if owner != "" && owner == orgID {
					return fakeRow{scanFn: func(dest ...any) error {
						*(dest[0].(*int)) = 1
						return nil
					}}
				}
				return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
		queryFn: func(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
			if strings.Contains(sql, "FROM fleet_team_orgs") {
				orgID, _ := args[0].(string)
				rows := [][]interface{}{}
				for teamID, owner := range map[int]string{1: testIsoOrgA, 2: testIsoOrgB} {
					if owner == orgID {
						rows = append(rows, []interface{}{teamID})
					}
				}
				return &fakeQueryRows{rows: rows}, nil
			}
			return &fakeQueryRows{}, nil
		},
	}
}

func twoTeamFleet() *fakeFleet {
	return &fakeFleet{
		listTeamsFn: func(_ context.Context) ([]fleet.Team, error) {
			return []fleet.Team{{ID: 1, Name: "team-a"}, {ID: 2, Name: "team-b"}}, nil
		},
	}
}

func TestAPITokenRequireFleetTeamForeign404(t *testing.T) {
	h := NewHandler(teamOrgDB(), &fakeKeycloak{}, twoTeamFleet(), zap.NewNop())

	// Own org's team: allowed.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/policies", nil).WithContext(apiTokenCtx(testIsoOrgA))
	if !h.requireFleetTeamInCallerOrg(rec, req, 1) {
		t.Fatalf("super-admin token on own-org team: expected allow, got %d — body: %s", rec.Code, rec.Body.String())
	}

	// Another org's team: 404, like any non-admin caller.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/teams/2/policies", nil).WithContext(apiTokenCtx(testIsoOrgA))
	if h.requireFleetTeamInCallerOrg(rec, req, 2) {
		t.Fatal("super-admin token on foreign team: expected deny, got allow")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("super-admin token on foreign team: expected 404, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestAPITokenListTeamsSeesOwnOrgOnly(t *testing.T) {
	h := NewHandler(teamOrgDB(), &fakeKeycloak{}, twoTeamFleet(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil).WithContext(apiTokenCtx(testIsoOrgA))
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool              `json:"success"`
		Data    ListTeamsResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Teams) != 1 || resp.Data.Teams[0].ID != 1 {
		t.Fatalf("super-admin token: expected only own-org team 1, got %+v", resp.Data.Teams)
	}
}

func TestHumanAdminListTeamsSeesAll(t *testing.T) {
	h := NewHandler(teamOrgDB(), &fakeKeycloak{}, twoTeamFleet(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil).WithContext(humanAdminCtx(testIsoOrgA))
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool              `json:"success"`
		Data    ListTeamsResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Teams) != 2 {
		t.Fatalf("human system admin: expected both teams, got %+v", resp.Data.Teams)
	}
}

func TestAPITokenListGroupsFilteredToOwnOrg(t *testing.T) {
	idA, nameA := "group-a", "Team A"
	idB, nameB := "group-b", "Team B"
	kc := &fakeKeycloak{
		listGroupsFn: func(_ context.Context) ([]*gocloak.Group, error) {
			return []*gocloak.Group{
				{ID: &idA, Name: &nameA, Attributes: &map[string][]string{"org_id": {testIsoOrgA}}},
				{ID: &idB, Name: &nameB, Attributes: &map[string][]string{"org_id": {testIsoOrgB}}},
			}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil).WithContext(apiTokenCtx(testIsoOrgA))
	rec := httptest.NewRecorder()
	h.ListGroups(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, nameA) {
		t.Errorf("super-admin token: expected own-org group %q visible, got: %s", nameA, body)
	}
	if strings.Contains(body, nameB) {
		t.Errorf("super-admin token: foreign group %q must be hidden, got: %s", nameB, body)
	}
}

func TestAPITokenListRealmRolesEmpty(t *testing.T) {
	roleID, roleName := "role-1", "freecloud-admin"
	kc := &fakeKeycloak{
		listRealmRolesFn: func(_ context.Context) ([]*gocloak.Role, error) {
			return []*gocloak.Role{{ID: &roleID, Name: &roleName}}, nil
		},
	}
	h := NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/roles", nil).WithContext(apiTokenCtx(testIsoOrgA))
	rec := httptest.NewRecorder()
	h.ListRealmRoles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), roleName) {
		t.Errorf("super-admin token: realm roles must be hidden, got: %s", rec.Body.String())
	}
}
