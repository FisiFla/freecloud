package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

func TestM09_PortalRequestAccess_RejectsNonUUIDApp(t *testing.T) {
	// Production: PortalRequestAccess → ValidateUserID(appId) after auth.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessRequestPayload{AppID: "not-uuid", Reason: "need"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/portal/access-requests", bytes.NewReader(body))
	req = req.WithContext(middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Role: middleware.RoleEndUser,
	}))
	req = req.WithContext(middleware.SetOrgContext(req.Context(), &middleware.OrgContext{
		OrgID: middleware.DefaultOrgID, Role: "member",
	}))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.PortalRequestAccess(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
