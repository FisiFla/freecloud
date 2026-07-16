package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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

// TestKeyForTokenManyUnknownKidsSingleUpstreamFetch is the H2 regression
// guard: an attacker sending many concurrent requests with distinct,
// JWKS-unknown `kid` header values (read from the unverified token header
// before any signature check) must not force one upstream JWKS fetch per
// request. Before the fix, keyForToken refreshed while holding a.mu.Lock()
// with no dedup/rate-limit, so N concurrent unknown kids caused N fetches
// (and serialized the whole API behind the lock + a synchronous 5s HTTP
// call). After the fix, single-flighting + a minimum refresh interval bound
// this to about one fetch regardless of N.
func TestKeyForTokenManyUnknownKidsSingleUpstreamFetch(t *testing.T) {
	privKey, jwksPayload, _, _ := generateTestKeyAndJWKS(t)

	var fetchCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwksPayload))
	}))
	defer srv.Close()

	issuer := srv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(srv.URL, "freecloud", "freecloud-dashboard")

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const n = 20
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			now := time.Now()
			tokenStr := signedToken(t, privKey, fmt.Sprintf("unknown-kid-%d", i), jwt.MapClaims{
				"sub":                "attacker",
				"iss":                issuer,
				"aud":                "freecloud-dashboard",
				"azp":                "freecloud-dashboard",
				"preferred_username": "attacker",
				"realm_access":       map[string]interface{}{"roles": []string{}},
				"exp":                float64(now.Add(1 * time.Hour).Unix()),
				"iat":                float64(now.Unix()),
			})
			<-start
			req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
			req.Header.Set("Authorization", "Bearer "+tokenStr)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("unknown kid %d: expected 401, got %d", i, rec.Code)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&fetchCount); got > 3 {
		t.Errorf("expected at most a couple of upstream JWKS fetches for %d concurrent unknown kids (single-flight + rate limit), got %d", n, got)
	}
}

// ---- H8: disabled-user JWT rejection ----

// fakeUserStatusDB is a minimal TokenDB fake for isUserDisabled's
// `SELECT disabled FROM users ...` single-column query shape.
type fakeUserStatusDB struct {
	disabled bool
	err      error
}

func (d fakeUserStatusDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return fakeUserStatusRow(d)
}

type fakeUserStatusRow struct {
	disabled bool
	err      error
}

func (r fakeUserStatusRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if p, ok := dest[0].(*bool); ok {
		*p = r.disabled
	}
	return nil
}

func newDisabledUserToken(t *testing.T, privKey *rsa.PrivateKey, kid, issuer, sub string) string {
	t.Helper()
	now := time.Now()
	return signedToken(t, privKey, kid, jwt.MapClaims{
		"sub":                sub,
		"iss":                issuer,
		"aud":                "freecloud-dashboard",
		"azp":                "freecloud-dashboard",
		"preferred_username": sub,
		"email":              sub + "@test.com",
		"realm_access":       map[string]interface{}{"roles": []string{"admin"}},
		"exp":                float64(now.Add(1 * time.Hour).Unix()),
		"iat":                float64(now.Unix()),
	})
}

// TestAuthMiddlewareRejectsDisabledUser is the H8 regression guard: a
// cryptographically-valid, unexpired JWT belonging to a user whose local
// `users` row has disabled=true (set by Offboard) must be rejected.
func TestAuthMiddlewareRejectsDisabledUser(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)
	issuer := jwksSrv.URL + "/realms/freecloud"

	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")
	am.userStatusDB = fakeUserStatusDB{disabled: true}

	tokenStr := newDisabledUserToken(t, privKey, kid, issuer, "offboarded-user")

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("a disabled user's request must never reach the protected handler")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("disabled user with valid JWT: expected 401, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareAllowsEnabledUser is the positive counterpart —
// disabled=false must still pass through, proving the check isn't just
// always rejecting.
func TestAuthMiddlewareAllowsEnabledUser(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)
	issuer := jwksSrv.URL + "/realms/freecloud"

	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")
	am.userStatusDB = fakeUserStatusDB{disabled: false}

	tokenStr := newDisabledUserToken(t, privKey, kid, issuer, "active-user")

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("enabled user: expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareAllowsUserNotTrackedLocally guards against a severe
// regression: the bootstrap admin created by POST /api/v1/setup (and any
// Keycloak user never onboarded/offboarded through FreeCloud) has NO row in
// the local `users` table. pgx.ErrNoRows must be treated as "not disabled",
// not as a lookup failure — otherwise the very first admin would be locked
// out of their own instance.
func TestAuthMiddlewareAllowsUserNotTrackedLocally(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)
	issuer := jwksSrv.URL + "/realms/freecloud"

	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")
	am.userStatusDB = fakeUserStatusDB{err: pgx.ErrNoRows}

	tokenStr := newDisabledUserToken(t, privKey, kid, issuer, "bootstrap-admin")

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("user with no local users row: expected 200 (allowed), got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareFailsClosedOnUserStatusLookupError verifies a genuine DB
// error (not "no rows") fails closed (401) rather than assuming the account
// is fine.
func TestAuthMiddlewareFailsClosedOnUserStatusLookupError(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)
	issuer := jwksSrv.URL + "/realms/freecloud"

	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")
	am.userStatusDB = fakeUserStatusDB{err: errors.New("connection reset")}

	tokenStr := newDisabledUserToken(t, privKey, kid, issuer, "some-user")

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("a lookup error must fail closed, not reach the protected handler")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("DB lookup error: expected 401 (fail closed), got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestNewAPITokenMiddlewareWiresUserStatusDB verifies NewAPITokenMiddleware
// wires its db into the wrapped AuthMiddleware's disabled-user check (H8) —
// this is the only production construction path (see main.go), so if this
// wiring silently regressed, disabled users would keep authenticating in
// production despite the unit tests above passing.
func TestNewAPITokenMiddlewareWiresUserStatusDB(t *testing.T) {
	base := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")
	db := fakeTokenDB{role: string(RoleSuperAdmin)}
	NewAPITokenMiddleware(base, db)
	if base.userStatusDB == nil {
		t.Fatal("expected NewAPITokenMiddleware to wire userStatusDB onto the base AuthMiddleware")
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

// Helpdesk/auditor/read-only API tokens must not receive OrgMembershipRoleAdmin
// (which would let them pass RequireOrgAdminOrSystemAdmin and manage org members).
func TestAPITokenMiddlewareOrgRoleElevation(t *testing.T) {
	base := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")

	t.Run("super-admin token is org-admin", func(t *testing.T) {
		apiMW := NewAPITokenMiddleware(base, fakeTokenDB{role: string(RoleSuperAdmin)})
		handler := apiMW.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			oc := GetOrgContext(r.Context())
			if oc == nil {
				t.Fatal("expected org context")
			}
			if oc.Role != OrgMembershipRoleAdmin {
				t.Fatalf("super-admin API token: expected org-admin, got %q", oc.Role)
			}
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		req.Header.Set("Authorization", "Bearer fc_testtoken")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	for _, role := range []Role{RoleHelpdesk, RoleAuditor, RoleReadOnly} {
		t.Run(string(role)+" token is not org-admin", func(t *testing.T) {
			apiMW := NewAPITokenMiddleware(base, fakeTokenDB{role: string(role)})
			handler := apiMW.Middleware(RequireOrgAdminOrSystemAdmin(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Fatalf("%s API token must not pass RequireOrgAdminOrSystemAdmin", role)
				}),
			))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/orgs/"+DefaultOrgID+"/members", nil)
			req.Header.Set("Authorization", "Bearer fc_testtoken")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s API token: expected 403, got %d — body: %s", role, rec.Code, rec.Body.String())
			}
		})
	}
}
