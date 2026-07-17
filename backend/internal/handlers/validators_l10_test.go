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

func TestL10_AssignUserToGroup_RejectsPathGroupID(t *testing.T) {
	// Production: AssignUserToGroup → ValidateOpaqueID(groupId) before Keycloak.
	// User validation passes; groupId path fails before Keycloak.
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AssignUserToGroupRequest{GroupID: "g/../x"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/u/groups", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignUserToGroup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
