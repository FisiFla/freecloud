package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// findCookie searches the recorder's response for a named cookie.
func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	resp := rec.Result()
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// trustedDeviceIdentityRequest builds a request that passes the M4
// Content-Type + Origin checks, so tests below it can focus on the
// pre-existing behavior those checks now gate.
func trustedDeviceIdentityRequest(body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", deviceIdentityTrustedOrigin())
	return req
}

func TestSetDeviceIdentityCookie_NilDB(t *testing.T) {
	// setupTestHandler uses nil DB.
	h := setupTestHandler(t)
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "tok"})
	req := trustedDeviceIdentityRequest(body)
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil DB: expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDeviceIdentityCookie_MissingToken(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(map[string]string{})
	req := trustedDeviceIdentityRequest(body)
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing token: expected 400, got %d", rec.Code)
	}
}

func TestSetDeviceIdentityCookie_EmptyToken(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: ""})
	req := trustedDeviceIdentityRequest(body)
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty token: expected 400, got %d", rec.Code)
	}
}

// ---- M4: Content-Type + Origin/Referer checks ----

func TestSetDeviceIdentityCookie_RejectsNonJSONContentType(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "tok"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", deviceIdentityTrustedOrigin())
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("non-JSON content type: expected 415, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDeviceIdentityCookie_RejectsMissingOriginAndReferer(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "tok"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Origin, no Referer — a legitimate browser fetch always sends at
	// least one; a cross-site form POST typically sends neither.
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("missing origin/referer: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDeviceIdentityCookie_RejectsMismatchedOrigin(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "tok"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("mismatched origin: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDeviceIdentityCookie_AllowsRefererFallback(t *testing.T) {
	// No Origin header, but a Referer matching the trusted origin — should
	// pass the M4 gate and reach the (nil-DB) 503, not a 403.
	h := setupTestHandler(t)
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "tok"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", deviceIdentityTrustedOrigin()+"/some/page")
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("matching referer: expected to pass through to 503 (nil db), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDeviceIdentityCookie_TokenNotFound(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "unknown-token"})
	req := trustedDeviceIdentityRequest(body)
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown token: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestSetDeviceIdentityCookie_LooksUpByHash proves the lookup hashes the
// presented token (M3) rather than matching it as plaintext — the fake DB
// asserts the query argument is the sha256 hex digest, not the raw token.
func TestSetDeviceIdentityCookie_LooksUpByHash(t *testing.T) {
	const plaintext = "valid-token"
	var gotArg string
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if len(args) > 0 {
				gotArg, _ = args[0].(string)
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: plaintext})
	req := trustedDeviceIdentityRequest(body)
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if gotArg == plaintext {
		t.Fatal("device-identity looked up the plaintext token, not its hash")
	}
	if gotArg != enrollmentTokenHash(plaintext) {
		t.Errorf("expected lookup by sha256 hash %q, got %q", enrollmentTokenHash(plaintext), gotArg)
	}
}

func TestSetDeviceIdentityCookie_HostIDNil(t *testing.T) {
	// Token found (used_at IS NOT NULL) but used_by_host_id is NULL.
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				// dest[0] is *(*string) — the pointer-to-pointer. Leave it as nil.
				if p, ok := dest[0].(**string); ok {
					*p = nil
				}
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "pending-token"})
	req := trustedDeviceIdentityRequest(body)
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("nil host: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDeviceIdentityCookie_Success(t *testing.T) {
	hostID := "host-abc-123"
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			if !strings.Contains(sql, "expires_at > NOW()") {
				t.Errorf("expected the M3 expires_at guard in the lookup query, got: %s", sql)
			}
			return fakeRow{scanFn: func(dest ...any) error {
				if p, ok := dest[0].(**string); ok {
					*p = &hostID
				}
				return nil
			}}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "valid-token"})
	req := trustedDeviceIdentityRequest(body)
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("success: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	c := findCookie(rec, deviceCookieName)
	if c == nil {
		t.Fatal("expected freecloud-device-id cookie to be set")
	}
	if c.Value != hostID {
		t.Errorf("cookie value: got %q, want %q", c.Value, hostID)
	}
	if !c.HttpOnly {
		t.Error("cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite: got %v, want Lax", c.SameSite)
	}
	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("response data type = %T, want object", resp.Data)
	}
	if _, ok := data["hostId"]; ok {
		t.Fatal("response body must not expose hostId")
	}
}

func TestSafePrefix(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 3, "hel…"},
		{"hi", 10, "hi"},
		{"", 5, ""},
		{"abcdefgh", 8, "abcdefgh"},
	}
	for _, tt := range tests {
		got := safePrefix(tt.s, tt.n)
		if got != tt.want {
			t.Errorf("safePrefix(%q,%d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}
