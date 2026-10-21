package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestAudienceAccepted covers the audience rules of issue #124:
//
//   - `aud` containing the configured audience is always accepted.
//   - `azp` is accepted ONLY as a bounded exception for a stock Keycloak
//     realm whose `aud` is the default account audience (or absent).
//   - A token whose `aud` names a DIFFERENT resource server is rejected even
//     when its `azp` matches the configured audience.
func TestAudienceAccepted(t *testing.T) {
	const audience = "freecloud-dashboard"

	cases := []struct {
		name   string
		claims jwt.MapClaims
		want   bool
	}{
		{
			name:   "aud string matches",
			claims: jwt.MapClaims{"aud": audience, "azp": audience},
			want:   true,
		},
		{
			name:   "aud array contains audience",
			claims: jwt.MapClaims{"aud": []interface{}{"other", audience}},
			want:   true,
		},
		{
			name:   "aud absent and azp matches",
			claims: jwt.MapClaims{"azp": audience},
			want:   true,
		},
		{
			name:   "default account audience with matching azp",
			claims: jwt.MapClaims{"aud": []interface{}{"account"}, "azp": audience},
			want:   true,
		},
		{
			name:   "aud names a different resource server",
			claims: jwt.MapClaims{"aud": []interface{}{"other-resource-server"}, "azp": audience},
			want:   false,
		},
		{
			name:   "aud names a different resource server alongside account",
			claims: jwt.MapClaims{"aud": []interface{}{"account", "other-resource-server"}, "azp": audience},
			want:   false,
		},
		{
			name:   "aud missing entirely",
			claims: jwt.MapClaims{},
			want:   false,
		},
		{
			name:   "azp does not match",
			claims: jwt.MapClaims{"aud": []interface{}{"account"}, "azp": "someone-else"},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := audienceAccepted(tc.claims, audience); got != tc.want {
				t.Errorf("audienceAccepted(%v) = %v, want %v", tc.claims, got, tc.want)
			}
		})
	}
}

// TestAuthMiddlewareRejectsForeignAudienceWithMatchingAzp is the end-to-end
// regression test for issue #124: a token minted for a different resource
// server must be rejected even when its `azp` names our own client.
func TestAuthMiddlewareRejectsForeignAudienceWithMatchingAzp(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)

	issuer := jwksSrv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")

	now := time.Now()
	tokenStr := signedToken(t, privKey, kid, jwt.MapClaims{
		"sub":   "attacker",
		"iss":   issuer,
		"aud":   []interface{}{"some-other-resource-server"},
		"azp":   "freecloud-dashboard",
		"exp":   float64(now.Add(1 * time.Hour).Unix()),
		"iat":   float64(now.Unix()),
		"scope": "openid",
	})

	handler := am.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("foreign audience with matching azp: expected 401, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddlewareAcceptsDefaultAccountAudience proves the bounded azp
// exception still lets a stock Keycloak realm (no audience mapper) work.
func TestAuthMiddlewareAcceptsDefaultAccountAudience(t *testing.T) {
	privKey, jwksPayload, kid, _ := generateTestKeyAndJWKS(t)
	jwksSrv := startJWKSServer(t, jwksPayload)

	issuer := jwksSrv.URL + "/realms/freecloud"
	am := NewAuthMiddleware(jwksSrv.URL, "freecloud", "freecloud-dashboard")

	now := time.Now()
	tokenStr := signedToken(t, privKey, kid, jwt.MapClaims{
		"sub": "alice",
		"iss": issuer,
		"aud": []interface{}{"account"},
		"azp": "freecloud-dashboard",
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

	if rec.Code != http.StatusOK {
		t.Errorf("default account audience: expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

// TestNewAuthMiddlewareTrimsTrailingSlash proves a KEYCLOAK_URL with a
// trailing slash yields the SAME issuer as one without (issue #132): the
// issuer is built by concatenation, so an untrimmed URL produced
// "https://kc/realms/…" with a double slash and every token was rejected.
func TestNewAuthMiddlewareTrimsTrailingSlash(t *testing.T) {
	withSlash := NewAuthMiddleware("https://kc.example.com/", "freecloud", "freecloud-dashboard")
	withoutSlash := NewAuthMiddleware("https://kc.example.com", "freecloud", "freecloud-dashboard")

	if withSlash.expectedIssuer != withoutSlash.expectedIssuer {
		t.Errorf("expected issuer must not depend on a trailing slash: %q vs %q",
			withSlash.expectedIssuer, withoutSlash.expectedIssuer)
	}
	want := "https://kc.example.com/realms/freecloud"
	if withSlash.expectedIssuer != want {
		t.Errorf("expected issuer %q, got %q", want, withSlash.expectedIssuer)
	}
}

// TestNewAuthMiddlewareTrimsTrailingSlashForJWKS proves the JWKS URL is
// built from the trimmed base URL too.
func TestNewAuthMiddlewareTrimsTrailingSlashForJWKS(t *testing.T) {
	am := NewAuthMiddleware("https://kc.example.com/", "freecloud", "freecloud-dashboard")
	want := "https://kc.example.com/realms/freecloud/protocol/openid-connect/certs"
	got := am.keycloakURL + "/realms/" + am.realm + "/protocol/openid-connect/certs"
	if got != want {
		t.Errorf("expected JWKS URL %q, got %q", want, got)
	}
}
