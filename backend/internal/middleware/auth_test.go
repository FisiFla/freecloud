package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
		{"/api/v1/users", true},
		{"/api/v1/audit-logs", true},
		{"/api/v1/auth/device-check", false},
		{"/api/v1/apps", true},
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
	//lint:ignore SA1012 deliberately passing nil to exercise GetClaims' nil-context guard
	if c := GetClaims(nil); c != nil {
		t.Error("expected nil claims from nil context")
	}
}

func TestAuthMiddlewareInvalidToken(t *testing.T) {
	am := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")
	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer this-is-not-a-valid-jwt")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid JWT, got %d", rec.Code)
	}
}

// generateTestKeyAndJWKS creates a real RSA key pair and returns the private key,
// a JWKS JSON payload (including a kid), the kid, and the expected issuer.
func generateTestKeyAndJWKS(t *testing.T) (*rsa.PrivateKey, string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	kid := "test-key-1"
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	jwksPayload := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"%s","use":"sig","alg":"RS256","n":"%s","e":"%s"}]}`, kid, n, e)
	return key, jwksPayload, kid, ""
}

// startJWKSServer starts an httptest.Server serving the given JWKS payload
// and returns the server along with the expected issuer URL.
func startJWKSServer(t *testing.T, jwksPayload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(jwksPayload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// signedToken creates a signed JWT with the given claims and kid, signed by privKey.
func signedToken(t *testing.T, privKey *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(privKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return s
}

// TestAuthMiddlewareAdminBoundary verifies an admin token reaches /api/v1/users.
func TestAuthMiddlewareAdminBoundary(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)

	issuer := jwksSrv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")

	now := time.Now()
	tokenStr := signedToken(t, privKey, kid, jwt.MapClaims{
		"sub":               "admin-user",
		"iss":               issuer,
		"aud":               "freecloud-dashboard",
		"azp":               "freecloud-dashboard",
		"preferred_username": "admin-user",
		"email":             "admin@test.com",
		"realm_access": map[string]interface{}{
			"roles": []string{"admin"},
		},
		"exp": float64(now.Add(1 * time.Hour).Unix()),
		"iat": float64(now.Unix()),
	})

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil {
			t.Error("expected claims in context")
		} else if !claims.IsAdmin {
			t.Error("expected admin claims")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("admin token on /api/v1/users: expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareNonAdminBlocked verifies a non-admin token gets 403 on /api/v1/users.
func TestAuthMiddlewareNonAdminBlocked(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)

	issuer := jwksSrv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")

	now := time.Now()
	tokenStr := signedToken(t, privKey, kid, jwt.MapClaims{
		"sub":               "regular-user",
		"iss":               issuer,
		"aud":               "freecloud-dashboard",
		"azp":               "freecloud-dashboard",
		"preferred_username": "regular-user",
		"email":             "user@test.com",
		"realm_access": map[string]interface{}{
			"roles": []string{"user"},
		},
		"exp": float64(now.Add(1 * time.Hour).Unix()),
		"iat": float64(now.Unix()),
	})

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin token on /api/v1/users: expected 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareWrongAudience verifies a token with the wrong audience gets 401.
func TestAuthMiddlewareWrongAudience(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)

	issuer := jwksSrv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")

	now := time.Now()
	tokenStr := signedToken(t, privKey, kid, jwt.MapClaims{
		"sub":               "user",
		"iss":               issuer,
		"aud":               "wrong-audience",
		"azp":               "wrong-audience",
		"preferred_username": "user",
		"email":             "user@test.com",
		"realm_access": map[string]interface{}{
			"roles": []string{"admin"},
		},
		"exp": float64(now.Add(1 * time.Hour).Unix()),
		"iat": float64(now.Unix()),
	})

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong audience token: expected 401, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareWrongIssuer verifies a token with the wrong issuer gets 401.
func TestAuthMiddlewareWrongIssuer(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)

	// Create middleware but manually override expectedIssuer to cause mismatch
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")
	// The middleware expects jwksSrv.URL + "/realms/freecloud"
	wrongIssuer := "http://evil.example.com/realms/freecloud"

	now := time.Now()
	tokenStr := signedToken(t, privKey, kid, jwt.MapClaims{
		"sub":               "user",
		"iss":               wrongIssuer,
		"aud":               "freecloud-dashboard",
		"azp":               "freecloud-dashboard",
		"preferred_username": "user",
		"email":             "user@test.com",
		"realm_access": map[string]interface{}{
			"roles": []string{"admin"},
		},
		"exp": float64(now.Add(1 * time.Hour).Unix()),
		"iat": float64(now.Unix()),
	})

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong issuer token: expected 401, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareExpiredToken verifies an expired token is rejected.
func TestAuthMiddlewareExpiredToken(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)

	issuer := jwksSrv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")

	now := time.Now()
	tokenStr := signedToken(t, privKey, kid, jwt.MapClaims{
		"sub":               "user",
		"iss":               issuer,
		"aud":               "freecloud-dashboard",
		"azp":               "freecloud-dashboard",
		"preferred_username": "user",
		"email":             "user@test.com",
		"realm_access": map[string]interface{}{
			"roles": []string{"admin"},
		},
		"exp": float64(now.Add(-1 * time.Hour).Unix()), // expired
		"iat": float64(now.Add(-2 * time.Hour).Unix()),
	})

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expired token: expected 401, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareWrongKey verifies a token signed with an unknown key is rejected.
func TestAuthMiddlewareWrongKey(t *testing.T) {
	// Generate two different key pairs
	_, jwksPayload, _, _ := generateTestKeyAndJWKS(t) // first key — the JWKS server serves this one
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate wrong key: %v", err)
	}

	jwksSrv := startJWKSServer(t, jwksPayload)
	defer jwksSrv.Close()

	issuer := jwksSrv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")

	now := time.Now()
	// Sign with the wrong (unknown) private key. Use a kid that won't match
	// anything in the JWKS, forcing a refresh and then a "no key for kid" failure.
	tokenStr := signedToken(t, wrongKey, "unknown-kid", jwt.MapClaims{
		"sub":               "user",
		"iss":               issuer,
		"aud":               "freecloud-dashboard",
		"azp":               "freecloud-dashboard",
		"preferred_username": "user",
		"email":             "user@test.com",
		"realm_access": map[string]interface{}{
			"roles": []string{"admin"},
		},
		"exp": float64(now.Add(1 * time.Hour).Unix()),
		"iat": float64(now.Unix()),
	})

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong key token: expected 401, got %d — body: %s", rec.Code, rec.Body.String())
	}
}
