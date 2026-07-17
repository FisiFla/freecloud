package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestL05_AccessEval_RejectsPathDeviceID(t *testing.T) {
	// Production: after cookie unwrap path, bare deviceId must pass ValidateHostID.
	h := NewHandler(nil, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(AccessEvalRequest{
		UserID:   "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		DeviceID: "../etc/passwd",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/access/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.EvaluateAccess(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 deny, got %d", rec.Code)
	}
	var resp struct {
		Data AccessEvalResponse `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Data.Allow {
		t.Fatal("expected allow=false for path device id")
	}
}
