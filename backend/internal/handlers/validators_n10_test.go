package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func TestN10_PortalRemoveMFAFactor_RejectsPathCredID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/portal/me/mfa/factors/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("credId", "a/../b")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: middleware.RoleEndUser,
	}))
	rec := httptest.NewRecorder()
	h.PortalRemoveMFAFactor(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestN10_CompleteCampaign_RejectsNonUUID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns/x/complete", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "not-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CompleteCampaign(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
