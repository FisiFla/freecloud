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

func TestL09_AddOrgMember_RejectsNonUUID(t *testing.T) {
	// Production: AddOrgMember → ValidateUserID before membership insert.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AddOrgMemberRequest{UserID: "not-uuid", Role: "member"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/x/members", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgId", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AddOrgMember(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
