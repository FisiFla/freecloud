package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func TestI05_RemoteLock_RejectsPathDeviceID(t *testing.T) {
	// Production: RemoteLock → ValidateHostID before org/Fleet.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/x/lock", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "../etc/passwd")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.RemoteLock(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path device id, got %d: %s", rec.Code, rec.Body.String())
	}
}
