package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func TestI07_RemoteRestart_RejectsDotDeviceID(t *testing.T) {
	// Production: RemoteRestart → ValidateHostID before org/Fleet.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/x/restart", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", ".hidden")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.RemoteRestart(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for dot device id, got %d: %s", rec.Code, rec.Body.String())
	}
}
