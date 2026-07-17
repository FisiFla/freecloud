package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/fleet"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func TestI04_ListPolicies_NonAdminGetsEmpty(t *testing.T) {
	// Production path: non–system-admin never sees Fleet global policies inventory.
	called := false
	f := &fakeFleet{
		listPoliciesFn: func(ctx context.Context) ([]fleet.Policy, error) {
			called = true
			return []fleet.Policy{{ID: "p1", Name: "secret"}}, nil
		},
	}
	h := NewHandler(nil, &fakeKeycloak{}, f, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/policies", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{Sub: "hd", Role: middleware.RoleHelpdesk}))
	rec := httptest.NewRecorder()
	h.ListPolicies(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("Fleet ListPolicies should still be called (upstream failure path)")
	}
	var resp struct {
		Data ListPoliciesResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Policies) != 0 {
		t.Fatalf("helpdesk must get empty policies, got %+v", resp.Data.Policies)
	}
}
