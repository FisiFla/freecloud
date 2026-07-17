package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestK06_EnrollmentCallback_RejectsPathHostID(t *testing.T) {
	// Production: FleetEnrollmentCallback → ValidateHostID before DB link.
	const secret = "topsecret"
	h := setupTestHandler(t)
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"tok","host_id":"../etc/passwd"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path host_id, got %d: %s", rec.Code, rec.Body.String())
	}
}
