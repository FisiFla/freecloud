package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestSetDeviceIdentityCookie_NilDB(t *testing.T) {
	// setupTestHandler uses nil DB.
	h := setupTestHandler(t)
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: "tok"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil DB: expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetDeviceIdentityCookie_MissingToken(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing token: expected 400, got %d", rec.Code)
	}
}

func TestSetDeviceIdentityCookie_EmptyToken(t *testing.T) {
	h := setupTestHandler(t)
	body, _ := json.Marshal(deviceIdentityRequest{EnrollmentToken: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty token: expected 400, got %d", rec.Code)
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetDeviceIdentityCookie(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown token: expected 404, got %d: %s", rec.Code, rec.Body.String())
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollment/device-identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
