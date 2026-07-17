package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestK07_AccessEval_RejectsInvalidUserID(t *testing.T) {
	// Production: EvaluateAccess → ValidateUserID before DB lookup.
	// Fail-closed response is HTTP 200 allow=false (auth-time contract).
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{UserID: "not-a-uuid", AppID: "app-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 deny envelope, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data AccessEvalResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Allow {
		t.Fatal("expected allow=false for invalid userId")
	}
}
