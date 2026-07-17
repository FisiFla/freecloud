package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func TestL02_UnassignUserFromGroup_RejectsPathGroupID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/u/groups/g", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rctx.URLParams.Add("groupId", "../admin")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.UnassignUserFromGroup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
