package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestJ10_CreateTeam_RejectsNameOverMaxDisplayLen(t *testing.T) {
	// Production: CreateTeam uses maxTeamDisplayNameLen before Fleet.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(CreateTeamRequest{Name: strings.Repeat("n", maxTeamDisplayNameLen+1)})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withOrgAdminCtx(req)
	rec := httptest.NewRecorder()
	h.CreateTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
