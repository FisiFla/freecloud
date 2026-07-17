package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestI02_ValidatePolicyID_RejectsControlChars(t *testing.T) {
	if err := ValidatePolicyID("pol\x01"); err == nil {
		t.Fatal("expected control char reject")
	}
	if err := ValidatePolicyID("policy-ok"); err != nil {
		t.Fatalf("ok: %v", err)
	}
}

func TestI02_AssignTeamPolicy_RejectsControlPolicyID(t *testing.T) {
	// Production: AssignTeamPolicy → ValidatePolicyID before Fleet.
	db := &fakeDB{queryRowFn: ownershipFoundQueryRowFn(nil)}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "bad\x00pol"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/1/policies", bytes.NewReader(body))
	req = withTeamID(req, "1")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for control policyId, got %d: %s", rec.Code, rec.Body.String())
	}
}
