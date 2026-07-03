package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
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
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	body, _ := json.Marshal(CreateTeamRequest{Name: "Security", Description: "Security team"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
