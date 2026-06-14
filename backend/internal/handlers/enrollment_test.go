package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func signFleet(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestEnrollmentCallbackRejectsBadSignature(t *testing.T) {
	h := setupTestHandler(t)
	h.SetFleetWebhookSecret("topsecret")
	body := []byte(`{"enrollment_token":"t","host_id":"h"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", "sha256=deadbeef")
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad signature, got %d: %s", rec.Code, rec.Body.String())
	}
}

// With no configured secret the callback must reject everything (fail closed),
// even a request that carries a syntactically valid-looking signature.
func TestEnrollmentCallbackFailsClosedWithoutSecret(t *testing.T) {
	h := setupTestHandler(t)
	body := []byte(`{"enrollment_token":"t","host_id":"h"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet("anything", body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when no webhook secret is configured, got %d", rec.Code)
	}
}

func TestEnrollmentCallbackUnknownToken(t *testing.T) {
	const secret = "topsecret"
	// Default fakeDB QueryRow returns ErrNoRows -> unknown token.
	h := NewHandler(&fakeDB{}, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"missing","host_id":"host-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEnrollmentCallbackMissingFields(t *testing.T) {
	const secret = "topsecret"
	h := NewHandler(&fakeDB{}, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetFleetWebhookSecret(secret)
	body := []byte(`{"enrollment_token":"","host_id":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/enrollment-callback", bytes.NewReader(body))
	req.Header.Set("X-Fleet-Signature", signFleet(secret, body))
	rec := httptest.NewRecorder()
	h.FleetEnrollmentCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing fields, got %d", rec.Code)
	}
}
