package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// withChiParam creates a request with chi URL param key=val set.
func withChiParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestGetMFAStatusOTP: user has OTP credential → otpEnabled=true.
func TestGetMFAStatusOTP(t *testing.T) {
	kc := &fakeKeycloak{
		getUserCredentialsFn: func(ctx context.Context, userID string) ([]string, error) {
			return []string{"otp"}, nil
		},
	}
	h := newHandlerNoDB(kc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/uid-1/mfa-status", nil)
	req = withChiParam(req, "id", "uid-1")
	rec := httptest.NewRecorder()
	h.GetMFAStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data MFAStatusResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.OTPEnabled {
		t.Error("expected OTPEnabled=true")
	}
	if env.Data.WebAuthnEnabled {
		t.Error("expected WebAuthnEnabled=false")
	}
}

// TestGetMFAStatusNone: user has no credentials and no pending actions.
func TestGetMFAStatusNone(t *testing.T) {
	h := newHandlerNoDB(&fakeKeycloak{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/uid-2/mfa-status", nil)
	req = withChiParam(req, "id", "uid-2")
	rec := httptest.NewRecorder()
	h.GetMFAStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestGetMFAStatusPendingTOTP: user has CONFIGURE_TOTP as required action.
func TestGetMFAStatusPendingTOTP(t *testing.T) {
	kc := &fakeKeycloak{
		getUserRequiredActionsFn: func(ctx context.Context, userID string) ([]string, error) {
			return []string{"CONFIGURE_TOTP"}, nil
		},
	}
	h := newHandlerNoDB(kc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/uid-3/mfa-status", nil)
	req = withChiParam(req, "id", "uid-3")
	rec := httptest.NewRecorder()
	h.GetMFAStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var env struct {
		Data MFAStatusResponse `json:"data"`
	}
	json.NewDecoder(rec.Body).Decode(&env)
	if !env.Data.OTPPending {
		t.Error("expected OTPPending=true")
	}
}

// TestRequireMFATOTP: valid totp type → 200, SetRequiredAction called.
func TestRequireMFATOTP(t *testing.T) {
	var capturedAction string
	kc := &fakeKeycloak{
		setRequiredActionFn: func(ctx context.Context, userID, action string) error {
			capturedAction = action
			return nil
		},
	}
	h := newHandlerNoDB(kc)

	body := `{"type":"totp"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/uid-4/require-mfa",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "id", "uid-4")
	rec := httptest.NewRecorder()
	h.RequireMFA(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedAction != "CONFIGURE_TOTP" {
		t.Errorf("expected action CONFIGURE_TOTP, got %q", capturedAction)
	}
}

// TestRequireMFAWebAuthn: valid webauthn type → 200.
func TestRequireMFAWebAuthn(t *testing.T) {
	kc := &fakeKeycloak{}
	h := newHandlerNoDB(kc)
	body := `{"type":"webauthn"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/uid-5/require-mfa",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "id", "uid-5")
	rec := httptest.NewRecorder()
	h.RequireMFA(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestRequireMFAInvalidType: unknown type → 400.
func TestRequireMFAInvalidType(t *testing.T) {
	h := newHandlerNoDB(&fakeKeycloak{})
	body := `{"type":"sms"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/uid-6/require-mfa",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "id", "uid-6")
	rec := httptest.NewRecorder()
	h.RequireMFA(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// TestRequireMFAMissingID → 400.
func TestRequireMFAMissingID(t *testing.T) {
	h := newHandlerNoDB(&fakeKeycloak{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users//require-mfa",
		bytes.NewBufferString(`{"type":"totp"}`))
	req.Header.Set("Content-Type", "application/json")
	// No chi param → empty id.
	rec := httptest.NewRecorder()
	h.RequireMFA(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// newHandlerNoDB creates a handler with no DB (nil) — sufficient for MFA tests.
func newHandlerNoDB(kc *fakeKeycloak) *Handler {
	return NewHandler(nil, kc, &fakeFleet{}, zap.NewNop())
}
