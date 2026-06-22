package handlers

// B1: Unit tests for MFA self-enrollment endpoints.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nerzal/gocloak/v13"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newPortalReq creates a test request with an end-user JWT claims stub so that
// portalUserID() returns "test-user-sub-123".
func newPortalReq(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	ctx := middleware.SetClaims(req.Context(), &middleware.JWTClaims{
		Sub:   "test-user-sub-123",
		Email: "tester@example.com",
		Role:  middleware.RoleEndUser,
	})
	return req.WithContext(ctx)
}

// newHandlerWithFakeDB creates a handler with the supplied kc + fakeDB.
func newHandlerWithFakeDB(kc *fakeKeycloak, db *fakeDB) *Handler {
	return NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
}

// ---------------------------------------------------------------------------
// GET /api/v1/portal/me/mfa/factors
// ---------------------------------------------------------------------------

func TestPortalMyMFAFactors_ListsOTPAndWebAuthn(t *testing.T) {
	otpType := "otp"
	waType := "webauthn"
	pwType := "password"
	id1 := "cred-otp-1"
	id2 := "cred-wa-1"
	id3 := "cred-pw-1"
	kc := &fakeKeycloak{
		getUserCredentialsFullFn: func(ctx context.Context, userID string) ([]*gocloak.CredentialRepresentation, error) {
			return []*gocloak.CredentialRepresentation{
				{ID: &id1, Type: &otpType},
				{ID: &id2, Type: &waType},
				{ID: &id3, Type: &pwType}, // must be filtered out
			}, nil
		},
	}
	h := newHandlerNoDB(kc)
	rec := httptest.NewRecorder()
	h.PortalMyMFAFactors(rec, newPortalReq(http.MethodGet, "/api/v1/portal/me/mfa/factors"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "otp") || !strings.Contains(body, "webauthn") {
		t.Errorf("expected both otp and webauthn in response, got: %s", body)
	}
	if strings.Contains(body, "password") {
		t.Errorf("password credential must be filtered out, got: %s", body)
	}
}

func TestPortalMyMFAFactors_NoAuth(t *testing.T) {
	h := newHandlerNoDB(&fakeKeycloak{})
	rec := httptest.NewRecorder()
	h.PortalMyMFAFactors(rec, httptest.NewRequest(http.MethodGet, "/api/v1/portal/me/mfa/factors", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/v1/portal/me/mfa/totp/enroll
// ---------------------------------------------------------------------------

func TestPortalEnrollTOTP_SetsRequiredAction(t *testing.T) {
	var capturedAction string
	kc := &fakeKeycloak{
		setRequiredActionFn: func(ctx context.Context, userID, action string) error {
			capturedAction = action
			return nil
		},
	}
	h := newHandlerNoDB(kc)
	rec := httptest.NewRecorder()
	h.PortalEnrollTOTP(rec, newPortalReq(http.MethodPost, "/api/v1/portal/me/mfa/totp/enroll"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedAction != "CONFIGURE_TOTP" {
		t.Errorf("expected CONFIGURE_TOTP, got %q", capturedAction)
	}
	if !strings.Contains(rec.Body.String(), "pending") {
		t.Error("expected 'pending' in response body")
	}
}

func TestPortalEnrollTOTP_NoAuth(t *testing.T) {
	h := newHandlerNoDB(&fakeKeycloak{})
	rec := httptest.NewRecorder()
	h.PortalEnrollTOTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/portal/me/mfa/totp/enroll", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/v1/portal/me/mfa/webauthn/enroll
// ---------------------------------------------------------------------------

func TestPortalEnrollWebAuthn_SetsRequiredAction(t *testing.T) {
	var capturedAction string
	kc := &fakeKeycloak{
		setRequiredActionFn: func(ctx context.Context, userID, action string) error {
			capturedAction = action
			return nil
		},
	}
	h := newHandlerNoDB(kc)
	rec := httptest.NewRecorder()
	h.PortalEnrollWebAuthn(rec, newPortalReq(http.MethodPost, "/api/v1/portal/me/mfa/webauthn/enroll"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if capturedAction != "webauthn-register" {
		t.Errorf("expected webauthn-register, got %q", capturedAction)
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/portal/me/mfa/factors/{credId}
// ---------------------------------------------------------------------------

func TestPortalRemoveMFAFactor_RemovesOwnOTPCredential(t *testing.T) {
	otpType := "otp"
	credID := "cred-otp-abc"
	var deletedCred string
	kc := &fakeKeycloak{
		getUserCredentialsFullFn: func(ctx context.Context, userID string) ([]*gocloak.CredentialRepresentation, error) {
			return []*gocloak.CredentialRepresentation{
				{ID: &credID, Type: &otpType},
			}, nil
		},
		deleteCredentialFn: func(ctx context.Context, userID, credentialID string) error {
			deletedCred = credentialID
			return nil
		},
	}
	h := newHandlerNoDB(kc)
	req := newPortalReq(http.MethodDelete, "/api/v1/portal/me/mfa/factors/"+credID)
	req = withChiParam(req, "credId", credID)
	rec := httptest.NewRecorder()
	h.PortalRemoveMFAFactor(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if deletedCred != credID {
		t.Errorf("expected deleted cred %q, got %q", credID, deletedCred)
	}
}

func TestPortalRemoveMFAFactor_RejectsPasswordCredential(t *testing.T) {
	pwType := "password"
	credID := "cred-pw-xyz"
	kc := &fakeKeycloak{
		getUserCredentialsFullFn: func(ctx context.Context, userID string) ([]*gocloak.CredentialRepresentation, error) {
			return []*gocloak.CredentialRepresentation{
				{ID: &credID, Type: &pwType},
			}, nil
		},
	}
	h := newHandlerNoDB(kc)
	req := newPortalReq(http.MethodDelete, "/api/v1/portal/me/mfa/factors/"+credID)
	req = withChiParam(req, "credId", credID)
	rec := httptest.NewRecorder()
	h.PortalRemoveMFAFactor(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for password credential, got %d", rec.Code)
	}
}

func TestPortalRemoveMFAFactor_MissingCredId(t *testing.T) {
	h := newHandlerNoDB(&fakeKeycloak{})
	// No chi credId param → empty string.
	rec := httptest.NewRecorder()
	h.PortalRemoveMFAFactor(rec, newPortalReq(http.MethodDelete, "/api/v1/portal/me/mfa/factors/"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/v1/portal/me/recovery-codes
// ---------------------------------------------------------------------------

func TestPortalGenerateRecoveryCodes_GeneratesAndStores(t *testing.T) {
	db := &fakeDB{
		beginFn: func(ctx context.Context) (pgx.Tx, error) {
			return &fakeTx{}, nil
		},
	}
	h := newHandlerWithFakeDB(&fakeKeycloak{}, db)
	rec := httptest.NewRecorder()
	h.PortalGenerateRecoveryCodes(rec, newPortalReq(http.MethodPost, "/api/v1/portal/me/recovery-codes"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "codes") || !strings.Contains(body, "createdAt") {
		t.Errorf("unexpected response shape: %s", body)
	}
}

func TestPortalGenerateRecoveryCodes_NoDB(t *testing.T) {
	h := newHandlerNoDB(&fakeKeycloak{})
	rec := httptest.NewRecorder()
	h.PortalGenerateRecoveryCodes(rec, newPortalReq(http.MethodPost, "/api/v1/portal/me/recovery-codes"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 without DB, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// GET /api/v1/portal/me/recovery-codes
// ---------------------------------------------------------------------------

func TestPortalRecoveryCodesStatus_HasCodes(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*int)) = 5
				return nil
			}}
		},
	}
	h := newHandlerWithFakeDB(&fakeKeycloak{}, db)
	rec := httptest.NewRecorder()
	h.PortalRecoveryCodesStatus(rec, newPortalReq(http.MethodGet, "/api/v1/portal/me/recovery-codes"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "true") {
		t.Errorf("expected hasRecoveryCodes=true, got: %s", rec.Body.String())
	}
}

func TestPortalRecoveryCodesStatus_NoCodes(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*int)) = 0
				return nil
			}}
		},
	}
	h := newHandlerWithFakeDB(&fakeKeycloak{}, db)
	rec := httptest.NewRecorder()
	h.PortalRecoveryCodesStatus(rec, newPortalReq(http.MethodGet, "/api/v1/portal/me/recovery-codes"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "false") {
		t.Errorf("expected hasRecoveryCodes=false, got: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// generateRecoveryCode + hashRecoveryCode
// ---------------------------------------------------------------------------

func TestGenerateRecoveryCode_UniqueAndHex(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		c, err := generateRecoveryCode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(c) != 10 {
			t.Errorf("expected 10-char hex code, got len=%d: %s", len(c), c)
		}
		if seen[c] {
			t.Errorf("duplicate code generated: %s", c)
		}
		seen[c] = true
	}
}

func TestHashRecoveryCode_Deterministic(t *testing.T) {
	h1 := hashRecoveryCode("abc123def4")
	h2 := hashRecoveryCode("abc123def4")
	if h1 != h2 {
		t.Errorf("hash is not deterministic: %q vs %q", h1, h2)
	}
	if h1 == "abc123def4" {
		t.Error("hash must differ from input")
	}
	if len(h1) != 64 { // SHA-256 = 32 bytes = 64 hex chars
		t.Errorf("expected 64-char hash, got %d: %s", len(h1), h1)
	}
}

// ---------------------------------------------------------------------------
// CheckAndConsumeRecoveryCode
// ---------------------------------------------------------------------------

func TestCheckAndConsumeRecoveryCode_ValidCode(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if len(dest) > 0 {
					if p, ok := dest[0].(*string); ok {
						*p = "some-uuid"
					}
				}
				return nil
			}}
		},
	}
	ok, err := CheckAndConsumeRecoveryCode(context.Background(), db, "user-1", "a1b2c3d4e5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected code to be consumed successfully")
	}
}

func TestCheckAndConsumeRecoveryCode_InvalidCode(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	ok, err := CheckAndConsumeRecoveryCode(context.Background(), db, "user-1", "wrong-code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected code to be rejected")
	}
}
