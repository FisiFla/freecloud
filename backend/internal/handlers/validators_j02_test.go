package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func TestJ02_GetDeviceCommandHistory_RejectsPathDeviceID(t *testing.T) {
	// Production: history path validates host id before org/DB.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/x/commands", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "../etc/passwd")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.GetDeviceCommandHistory(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
