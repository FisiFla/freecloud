package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsManagementEndpoint(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/onboard", true},
		{"/api/v1/offboard/some-id", true},
		{"/api/v1/apps/create", true},
		{"/api/v1/apps/some-id/assign", true},
		{"/api/v1/health", false},
		{"/api/v1/users", false},
		{"/api/v1/audit-logs", false},
		{"/api/v1/auth/device-check", false},
		{"/api/v1/apps", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isManagementEndpoint(tt.path)
			if got != tt.want {
				t.Errorf("isManagementEndpoint(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestAuthMiddlewareMissingToken(t *testing.T) {
	am := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing token, got %d", rec.Code)
	}
}

func TestAuthMiddlewareHealthSkips(t *testing.T) {
	am := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for health (no auth), got %d", rec.Code)
	}
}

func TestGetClaimsNil(t *testing.T) {
	if c := GetClaims(nil); c != nil {
		t.Error("expected nil claims from nil context")
	}
}

// TODO: Add tests for JWT validation that require generating test tokens:
//   - TestAuthAudienceCheck: verify audience validation (invalid aud → 401)
//   - TestAuthIssuerCheck: verify issuer validation (wrong iss → 401)
//   - TestAuthExpiredToken: verify expired JWT rejection
//   - TestAuthWrongKey: verify token signed with wrong key is rejected
//   - TestAuthManagementEndpointRequiresAdmin: verify admin endpoint rejects non-admin tokens
// These tests require generating RS256 JWTs with known keys or using a test fixture.
