package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestL07_EnrollmentCallback_RejectsOverlongToken(t *testing.T) {
	const secret = "topsecret"
	h := setupTestHandler(t)
	h.SetFleetWebhookSecret(secret)
	tok := strings.Repeat("t", maxEnrollmentTokenLen+1)
	body := []byte(`{"enrollment_token":"` + tok + `","host_id":"host-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for overlong token, got %d: %s", rec.Code, rec.Body.String())
	}
}
