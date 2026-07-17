package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParsePositiveTeamID(t *testing.T) {
	if id, err := ParsePositiveTeamID("42"); err != nil || id != 42 {
		t.Fatalf("ok: got %d %v", id, err)
	}
	for _, bad := range []string{"", "0", "-1", "abc", "12a", "9999999999", strings.Repeat("9", 20)} {
		if _, err := ParsePositiveTeamID(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestAssignTeamPolicy_RejectsOverlongTeamID(t *testing.T) {
	// Production path: path segment must not wrap/overflow into a valid team id.
	h := setupTestHandler(t)
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "pol"})
	// 10+ digits rejected before any org/fleet call
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/9999999999/policies", bytes.NewReader(body))
	req = withTeamID(req, "9999999999")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for overlong team id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAssignTeamPolicy_RejectsZeroTeamID(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(AssignTeamPolicyRequest{PolicyID: "pol"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/teams/0/policies", bytes.NewReader(body))
	req = withTeamID(req, "0")
	req = withOrgAdminCtx(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.AssignTeamPolicy(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for team id 0, got %d: %s", rec.Code, rec.Body.String())
	}
}
