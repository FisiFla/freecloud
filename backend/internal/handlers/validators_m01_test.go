package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func TestM01_RemoveOrgMember_RejectsNonUUIDUser(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/orgs/o/members/u", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgId", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rctx.URLParams.Add("userId", "not-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.RemoveOrgMember(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
