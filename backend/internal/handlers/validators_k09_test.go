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

func TestK09_PatchUser_RejectsNonUUID(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	fn := "Ada"
	body, _ := json.Marshal(PatchUserRequest{FirstName: &fn})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/x", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "not-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.PatchUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
