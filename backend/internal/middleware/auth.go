package middleware

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
}

type claimsKeyType struct{}

var claimsKey = claimsKeyType{}

// GetClaims retrieves JWT claims from the request context, if present.
func GetClaims(ctx context.Context) *JWTClaims {
	if c, ok := ctx.Value(claimsKey).(*JWTClaims); ok {
		return c
	}
	return nil
}

// AuthMiddleware validates JWTs against a Keycloak realm.
type AuthMiddleware struct {
	keycloakURL string
	realm       string
	mu          sync.RWMutex
	keys        []*rsa.PublicKey
	lastFetch   time.Time
}

// NewAuthMiddleware creates a new AuthMiddleware.
func NewAuthMiddleware(keycloakURL, realm string) *AuthMiddleware {
	return &AuthMiddleware{
		keycloakURL: keycloakURL,
		realm:       realm,
	}
}

func (a *AuthMiddleware) fetchKeys() ([]*rsa.PublicKey, error) {
	a.mu.RLock()
	if time.Since(a.lastFetch) < 5*time.Minute && len(a.keys) > 0 {
		keys := a.keys
		a.mu.RUnlock()
		return keys, nil
	}
	a.mu.RUnlock()

	a.mu.Lock()
	defer a.mu.Unlock()

	if time.Since(a.lastFetch) < 5*time.Minute && len(a.keys) > 0 {
		return a.keys, nil
	}

	url := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", a.keycloakURL, a.realm)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []struct {
			N string `json:"n"`
			E string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}

	var keys []*rsa.PublicKey
	for _, k := range jwks.Keys {
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

		key := &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: e,
		}
		keys = append(keys, key)
	}

	// Also try the PEM cert endpoint as fallback
	if len(keys) == 0 {
		certURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", a.keycloakURL, a.realm)
		resp2, err := http.Get(certURL)
		if err == nil {
			defer resp2.Body.Close()
			var certResp struct {
				Keys []struct {
					X5c []string `json:"x5c"`
				} `json:"keys"`
			}
			if json.NewDecoder(resp2.Body).Decode(&certResp) == nil {
				for _, k := range certResp.Keys {
					for _, certB64 := range k.X5c {
						certPEM := "-----BEGIN CERTIFICATE-----\n" + certB64 + "\n-----END CERTIFICATE-----"
						block, _ := pem.Decode([]byte(certPEM))
						if block == nil {
							continue
						}
						cert, err := x509.ParseCertificate(block.Bytes)
						if err != nil {
							continue
						}
						if pubKey, ok := cert.PublicKey.(*rsa.PublicKey); ok {
							keys = append(keys, pubKey)
						}
					}
				}
			}
		}
	}

	a.keys = keys
	a.lastFetch = time.Now()
	return keys, nil
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

		// Parse without verification first to extract claims
		parser := jwt.NewParser()
		token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"success":false,"error":"unauthorized: invalid token"}`))
			return
		}

		// Verify signature with Keycloak public keys
		keys, err := a.fetchKeys()
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"success":false,"error":"auth service temporarily unavailable"}`))
			return
		}

		var lastErr error
		verified := false
		for _, key := range keys {
			validated, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			errMsg := "unauthorized: token verification failed"
			if lastErr != nil {
				errMsg = "unauthorized: " + lastErr.Error()
			}
			w.Write([]byte(fmt.Sprintf(`{"success":false,"error":"%s"}`, errMsg)))
			return
		}

		next.ServeHTTP(w, r)
	})
}
