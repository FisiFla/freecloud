package handlers

// L3 lane: ListTeams must resolve the caller's org (and fail 403/500) BEFORE
// calling Fleet, like ListPolicies does — otherwise the Fleet call acts as an
// availability oracle for callers with no org at all.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// nonAdminNoOrgCtx is a helpdesk JWT with no resolved OrgContext (e.g. org
// resolution failed or found no membership).
func nonAdminNoOrgCtx() context.Context {
	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub:               "help-1",
		PreferredUsername: "help-1",
		Role:              middleware.RoleHelpdesk,
	})
	return context.WithValue(ctx, middleware.ActorIDKey, "help-1")
}

func TestListTeamsWithoutOrgFailsBeforeFleet(t *testing.T) {
	called := false
	ff := &fakeFleet{
		listTeamsFn: func(_ context.Context) ([]fleet.Team, error) {
			called = true
			return nil, errors.New("fleet down")
		},
	}
	h := NewHandler(nil, &fakeKeycloak{}, ff, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil).WithContext(nonAdminNoOrgCtx())
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("no-org caller with Fleet down: expected 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("Fleet must not be called for a caller with no org context")
	}
}

func TestListTeamsWithoutDBFailsBeforeFleet(t *testing.T) {
	called := false
	ff := &fakeFleet{
		listTeamsFn: func(_ context.Context) ([]fleet.Team, error) {
			called = true
			return []fleet.Team{{ID: 1, Name: "team-a"}}, nil
		},
	}
	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub: "help-1", PreferredUsername: "help-1", Role: middleware.RoleHelpdesk,
	})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: testIsoOrgA, Role: middleware.OrgMembershipRoleAdmin})
	h := NewHandler(nil, &fakeKeycloak{}, ff, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("nil db: expected 500, got %d — body: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Error("Fleet must not be called when the org mapping cannot be loaded")
	}
}

func TestListTeamsNonAdminHappyPathStillFiltered(t *testing.T) {
	ff := twoTeamFleet()
	h := NewHandler(teamOrgDB(), &fakeKeycloak{}, ff, zap.NewNop())

	ctx := middleware.SetClaims(context.Background(), &middleware.JWTClaims{
		Sub: "help-1", PreferredUsername: "help-1", Role: middleware.RoleHelpdesk,
	})
	ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{OrgID: testIsoOrgA, Role: middleware.OrgMembershipRoleAdmin})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ListTeams(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
}
