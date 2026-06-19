package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTClaims holds the claims we extract from the validated JWT.
type JWTClaims struct {
	Sub               string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	IsAdmin           bool   `json:"-"`
}

type claimsKeyType struct{}

var claimsKey = claimsKeyType{}

// GetClaims retrieves JWT claims from the request context, if present.
func GetClaims(ctx context.Context) *JWTClaims {
	if ctx == nil {
		return nil
	}
	if c, ok := ctx.Value(claimsKey).(*JWTClaims); ok {
		return c
	}
	return nil
}

// SetClaims stores JWT claims in the context for testing or manual injection.
func SetClaims(ctx context.Context, claims *JWTClaims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// isManagementEndpoint returns true if the path is a management API that requires admin access.
func isManagementEndpoint(path string) bool {
	mgmtExactPaths := map[string]bool{
		"/api/v1/users":      true,
		"/api/v1/apps":       true,
		"/api/v1/audit-logs": true,
		"/api/v1/groups":     true,
		"/api/v1/roles":      true,
		"/api/v1/compliance": true,
		"/api/v1/policies":   true,
	}
	if mgmtExactPaths[path] {
		return true
	}
	mgmtPrefixes := []string{
		"/api/v1/onboard",
		"/api/v1/offboard",
		"/api/v1/apps/",
		"/api/v1/users/",
		"/api/v1/audit-logs/",
		"/api/v1/groups/",
		"/api/v1/roles/",
		"/api/v1/devices/",
		"/api/v1/compliance",
		"/api/v1/policies",
	}
	for _, p := range mgmtPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// AuthMiddleware validates JWTs against a Keycloak realm.
type AuthMiddleware struct {
	keycloakURL    string
	realm          string
	audience       string
	expectedIssuer string
	httpClient     *http.Client
	mu             sync.RWMutex
	// keysByKid maps a JWT "kid" header to its parsed RSA public key.
	keysByKid map[string]*rsa.PublicKey
	lastFetch time.Time
}

// NewAuthMiddleware creates a new AuthMiddleware.
func NewAuthMiddleware(keycloakURL, realm, audience string) *AuthMiddleware {
	return &AuthMiddleware{
		keycloakURL:    keycloakURL,
		realm:          realm,
		audience:       audience,
		expectedIssuer: fmt.Sprintf("%s/realms/%s", keycloakURL, realm),
		// One reused client instead of allocating per fetch.
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// refreshJWKS fetches the realm's JWKS document and stores the parsed keys
// indexed by their "kid" header. Must be called with a.mu held.
func (a *AuthMiddleware) refreshJWKS() error {
	url := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", a.keycloakURL, a.realm)
	resp, err := a.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks fetch returned status %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	parsed := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}

		e := 0
		for _, b := range eBytes {
			e = e<<8 + int(b)
		}

		parsed[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: e,
		}
	}

	if len(parsed) == 0 {
		return fmt.Errorf("no valid RSA keys found in JWKS response")
	}

	a.keysByKid = parsed
	a.lastFetch = time.Now()
	return nil
}

// keyForToken returns the verification key matching the token's "kid" header.
// It refreshes the cached JWKS if the kid is unknown or the cache is stale/empty.
// Returns (nil, nil) when the token has no kid header — the caller then tries
// every cached key as a fallback.
func (a *AuthMiddleware) keyForToken(tokenString string) (*rsa.PublicKey, error) {
	// Parse just the header segment for the kid (no signature verification).
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if parts := strings.Split(tokenString, "."); len(parts) > 0 {
		if raw, err := base64.RawURLEncoding.DecodeString(parts[0]); err == nil {
			_ = json.Unmarshal(raw, &header)
		}
	}

	a.mu.RLock()
	if time.Since(a.lastFetch) < 5*time.Minute && len(a.keysByKid) > 0 {
		if header.Kid != "" {
			if key, ok := a.keysByKid[header.Kid]; ok {
				a.mu.RUnlock()
				return key, nil
			}
		}
		// kid not in cache but cache fresh — fall through and refresh once below.
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check after acquiring the write lock.
	if time.Since(a.lastFetch) < 5*time.Minute && len(a.keysByKid) > 0 {
		if header.Kid != "" {
			if key, ok := a.keysByKid[header.Kid]; ok {
				return key, nil
			}
		}
	}

	// Cache stale or kid unknown — refresh.
	if err := a.refreshJWKS(); err != nil {
		return nil, err
	}
	if header.Kid != "" {
		if key, ok := a.keysByKid[header.Kid]; ok {
			return key, nil
		}
		return nil, fmt.Errorf("no key found for kid %q after JWKS refresh", header.Kid)
	}
	// No kid in header: caller (Middleware) will try every key as a last resort.
	return nil, nil
}

// Middleware is an HTTP middleware that validates the Bearer token.
func (a *AuthMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip health endpoint
		if strings.HasPrefix(r.URL.Path, "/api/v1/health") {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"success":false,"error":"unauthorized: missing Bearer token"}`))
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		// Resolve the verification key by kid (fast path); falls back to trying
		// all cached keys if the token has no kid header.
		primary, err := a.keyForToken(tokenString)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized: "+err.Error())
			return
		}

		// Build the candidate key list: kid-matched key first, then any others.
		var candidates []*rsa.PublicKey
		if primary != nil {
			candidates = []*rsa.PublicKey{primary}
		}
		a.mu.RLock()
		for _, k := range a.keysByKid {
			if primary == nil || k != primary {
				candidates = append(candidates, k)
			}
		}
		a.mu.RUnlock()
		if len(candidates) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"success":false,"error":"auth service temporarily unavailable"}`))
			return
		}

		var lastErr error
		verified := false
		for _, key := range candidates {
			// Parse with a parser that requires `exp` and allows 30s clock skew.
			// Without WithExpirationRequired, a token lacking `exp` would be
			// accepted as never-expiring.
			parser := jwt.NewParser(
				jwt.WithExpirationRequired(),
				jwt.WithIssuedAt(),
				jwt.WithLeeway(30*time.Second),
			)
			validated, err := parser.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return key, nil
			})
			if err == nil && validated.Valid {
				verified = true
				if claims, ok := validated.Claims.(jwt.MapClaims); ok {
					jc := &JWTClaims{}
					if sub, ok := claims["sub"].(string); ok {
						jc.Sub = sub
					}
					if pu, ok := claims["preferred_username"].(string); ok {
						jc.PreferredUsername = pu
					}
					if email, ok := claims["email"].(string); ok {
						jc.Email = email
					}
					// Validate audience
					if a.audience != "" {
						audOK := false
						if aud, ok := claims["aud"].(string); ok && aud == a.audience {
							audOK = true
						}
						if auds, ok := claims["aud"].([]interface{}); ok {
							for _, v := range auds {
								if s, ok := v.(string); ok && s == a.audience {
									audOK = true
									break
								}
							}
						}
						// Also check azp (authorized party)
						if azp, ok := claims["azp"].(string); ok && azp == a.audience {
							audOK = true
						}
						if !audOK {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusUnauthorized)
							w.Write([]byte(`{"success":false,"error":"unauthorized: invalid audience"}`))
							return
						}
					}
					// Validate issuer
					if a.expectedIssuer != "" {
						if iss, ok := claims["iss"].(string); !ok || iss != a.expectedIssuer {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusUnauthorized)
							w.Write([]byte(`{"success":false,"error":"unauthorized: invalid issuer"}`))
							return
						}
					}
					// Check for admin role
					if realmAccess, ok := claims["realm_access"].(map[string]interface{}); ok {
						if roles, ok := realmAccess["roles"].([]interface{}); ok {
							for _, r := range roles {
								if role, ok := r.(string); ok && (role == "admin" || role == "freecloud-admin") {
									jc.IsAdmin = true
									break
								}
							}
						}
					}
					ctx := context.WithValue(r.Context(), claimsKey, jc)
					r = r.WithContext(ctx)
				}
				break
			}
			if err != nil {
				lastErr = err
			}
		}

		if !verified {
			msg := "token verification failed"
			if lastErr != nil {
				msg = lastErr.Error()
			}
			writeAuthError(w, http.StatusUnauthorized, "unauthorized: "+msg)
			return
		}

		// Admin authorization for management endpoints
		if isManagementEndpoint(r.URL.Path) {
			claims := GetClaims(r.Context())
			if claims == nil || !claims.IsAdmin {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"success":false,"error":"forbidden: admin access required"}`))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// writeAuthError writes a JSON error response using proper encoding so the
// message cannot break out of the JSON string (avoids the injection risk of
// fmt.Sprintf-based JSON construction when the message reflects attacker input).
func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	})
}
