package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestI01_ValidateHostID_RejectsControlChars(t *testing.T) {
	if err := ValidateHostID("host\x00id"); err == nil {
		t.Fatal("expected control char reject")
	}
	if err := ValidateHostID("ok-host-1"); err != nil {
		t.Fatalf("ok host: %v", err)
	}
}

func TestI01_MoveHostToTeam_RejectsControlHostID(t *testing.T) {
	// Production path: MoveHostToTeam → ValidateHostID before Fleet.
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(MoveHostRequest{HostIDs: []string{"bad\x01host"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/hosts", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MoveHostToTeam(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for control host id, got %d: %s", rec.Code, rec.Body.String())
	}
}
