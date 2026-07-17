package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func TestL04_AssignApp_RejectsPathAppID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AssignAppRequest{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/x/assign", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("appId", "../admin")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignApp(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
