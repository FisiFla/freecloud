package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
)

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
		"sub":                "admin-user",
		"iss":                issuer,
		"aud":                "freecloud-dashboard",
		"azp":                "freecloud-dashboard",
		"preferred_username": "admin-user",
		"email":              "admin@test.com",
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

// TestAuthMiddlewareNonAdminAuthenticated verifies auth middleware only
// authenticates and populates claims. Route-level middleware owns authorization.
func TestAuthMiddlewareNonAdminAuthenticated(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)

	issuer := jwksSrv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")

	now := time.Now()
	tokenStr := signedToken(t, privKey, kid, jwt.MapClaims{
		"sub":                "regular-user",
		"iss":                issuer,
		"aud":                "freecloud-dashboard",
		"azp":                "freecloud-dashboard",
		"preferred_username": "regular-user",
		"email":              "user@test.com",
		"realm_access": map[string]interface{}{
			"roles": []string{"user"},
		},
		"exp": float64(now.Add(1 * time.Hour).Unix()),
		"iat": float64(now.Unix()),
	})

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil {
			t.Fatal("expected claims in context")
		}
		if claims.Role != RoleEndUser {
			t.Fatalf("expected end-user role, got %q", claims.Role)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("non-admin token: expected 200 from auth-only middleware, got %d — body: %s", rec.Code, rec.Body.String())
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
		"sub":                "user",
		"iss":                issuer,
		"aud":                "wrong-audience",
		"azp":                "wrong-audience",
		"preferred_username": "user",
		"email":              "user@test.com",
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
		"sub":                "user",
		"iss":                wrongIssuer,
		"aud":                "freecloud-dashboard",
		"azp":                "freecloud-dashboard",
		"preferred_username": "user",
		"email":              "user@test.com",
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
		"sub":                "user",
		"iss":                issuer,
		"aud":                "freecloud-dashboard",
		"azp":                "freecloud-dashboard",
		"preferred_username": "user",
		"email":              "user@test.com",
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
		"sub":                "user",
		"iss":                issuer,
		"aud":                "freecloud-dashboard",
		"azp":                "freecloud-dashboard",
		"preferred_username": "user",
		"email":              "user@test.com",
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

// ---- RBAC tests ----

func TestResolveRole(t *testing.T) {
	tests := []struct {
		roles []string
		want  Role
	}{
		{[]string{"admin"}, RoleSuperAdmin},
		{[]string{"freecloud-admin"}, RoleSuperAdmin},
		{[]string{"freecloud-helpdesk"}, RoleHelpdesk},
		{[]string{"freecloud-auditor"}, RoleAuditor},
		{[]string{"freecloud-readonly"}, RoleReadOnly},
		{[]string{"some-other-role"}, RoleEndUser},
		{[]string{}, RoleEndUser},
		// super-admin beats helpdesk
		{[]string{"freecloud-helpdesk", "admin"}, RoleSuperAdmin},
	}
	for _, tt := range tests {
		got := resolveRole(tt.roles)
		if got != tt.want {
			t.Errorf("resolveRole(%v) = %q, want %q", tt.roles, got, tt.want)
		}
	}
}

func TestHasPermissionDeniesNilClaims(t *testing.T) {
	if HasPermission(context.Background(), PermReadUsers) {
		t.Error("expected false when no claims in context")
	}
}

func TestHasPermissionMatrix(t *testing.T) {
	tests := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleSuperAdmin, PermManageUsers, true},
		{RoleHelpdesk, PermManageUsers, false},
		{RoleHelpdesk, PermOnboardOffboard, false},
		{RoleHelpdesk, PermSubmitApprovals, true},
		{RoleAuditor, PermOnboardOffboard, false},
		{RoleAuditor, PermExportAuditLogs, true},
		{RoleReadOnly, PermExportAuditLogs, false},
		{RoleReadOnly, PermReadUsers, true},
		{RoleEndUser, PermReadUsers, false},
		{RoleEndUser, PermSelfService, true},
	}
	for _, tt := range tests {
		ctx := SetClaims(context.Background(), &JWTClaims{Role: tt.role})
		got := HasPermission(ctx, tt.perm)
		if got != tt.want {
			t.Errorf("HasPermission(role=%s, perm=%s) = %v, want %v", tt.role, tt.perm, got, tt.want)
		}
	}
}

func TestRequirePermissionAllows(t *testing.T) {
	// Test RequirePermission in isolation — inject claims directly without going
	// through AuthMiddleware (which has the management-gate that would block auditor).
	claims := &JWTClaims{Role: RoleAuditor}
	ctx := SetClaims(context.Background(), claims)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/some-path", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	RequirePermission(PermReadAuditLogs)(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("auditor+PermReadAuditLogs: want 200, got %d — %s", rec.Code, rec.Body.String())
	}
}

func TestRequirePermissionDenies(t *testing.T) {
	// end-user cannot manage users.
	claims := &JWTClaims{Role: RoleEndUser}
	ctx := SetClaims(context.Background(), claims)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/some-path", nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	RequirePermission(PermManageUsers)(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("end-user+PermManageUsers: want 403, got %d", rec.Code)
	}
}

func TestRequirePermissionNoClaims(t *testing.T) {
	// No claims in context → 403.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/some-path", nil)
	rec := httptest.NewRecorder()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	RequirePermission(PermSelfService)(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no claims+PermSelfService: want 403, got %d", rec.Code)
	}
}

type fakeTokenDB struct {
	role string
	err  error
}

func (db fakeTokenDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return fakeTokenRow(db)
}

type fakeTokenRow struct {
	role string
	err  error
}

func (r fakeTokenRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	*(dest[1].(*string)) = r.role
	*(dest[2].(*string)) = "ci"
	*(dest[3].(*string)) = DefaultOrgID
	*(dest[4].(**time.Time)) = nil
	*(dest[5].(**time.Time)) = nil
	return nil
}

func TestAPITokenMiddlewareStoredRoleAllowsPermission(t *testing.T) {
	base := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")
	apiMW := NewAPITokenMiddleware(base, fakeTokenDB{role: string(RoleSuperAdmin)})
	handler := apiMW.Middleware(RequirePermission(PermManageAPITokens)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				t.Fatal("expected API token claims")
			}
			if claims.Role != RoleSuperAdmin {
				t.Fatalf("expected super-admin role, got %q", claims.Role)
			}
			if claims.PreferredUsername != "api-token:ci" {
				t.Fatalf("expected service identity actor, got %q", claims.PreferredUsername)
			}
			w.WriteHeader(http.StatusOK)
		}),
	))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil)
	req.Header.Set("Authorization", "Bearer fc_testtoken")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("super-admin API token: expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestAPITokenMiddlewareRequiresBearerScheme(t *testing.T) {
	base := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")
	apiMW := NewAPITokenMiddleware(base, fakeTokenDB{role: string(RoleSuperAdmin)})
	handler := apiMW.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("bare API token must not reach the protected handler")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil)
	req.Header.Set("Authorization", "fc_testtoken")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bare API token: expected 401, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestAPITokenMiddlewareStoredRoleDeniesMissingPermission(t *testing.T) {
	base := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")
	apiMW := NewAPITokenMiddleware(base, fakeTokenDB{role: string(RoleReadOnly)})
	handler := apiMW.Middleware(RequirePermission(PermManageAPITokens)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil)
	req.Header.Set("Authorization", "Bearer fc_testtoken")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-only API token: expected 403, got %d — body: %s", rec.Code, rec.Body.String())
	}
}
