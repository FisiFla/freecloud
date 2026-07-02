package middleware

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
)

// Role is the internal RBAC role resolved from Keycloak realm roles.
type Role string

const (
	RoleSuperAdmin Role = "super-admin"
	RoleHelpdesk   Role = "helpdesk"
	RoleAuditor    Role = "auditor"
	RoleReadOnly   Role = "read-only"
	RoleEndUser    Role = "end-user"
)

// OrgMembershipRoleAdmin is the org_memberships.role value that grants
// org-scoped admin rights within one organization (C2 / Epic C multi-tenant).
// This is orthogonal to the global Role above: RoleSuperAdmin is a SYSTEM
// admin with cross-org reach resolved from the JWT's realm roles, while
// "org-admin" is a per-membership row in Postgres scoped to one org and
// resolved by OrgContextMiddleware into the request's OrgContext.Role.
const OrgMembershipRoleAdmin = "org-admin"

// Permission is a capability checked at the handler level.
type Permission string

const (
	PermManageUsers     Permission = "manage:users"
	PermOnboardOffboard Permission = "onboard:offboard"
	PermReadUsers       Permission = "read:users"
	PermManageApps      Permission = "manage:apps"
	PermReadApps        Permission = "read:apps"
	PermReadAuditLogs   Permission = "read:audit-logs"
	PermExportAuditLogs Permission = "export:audit-logs"
	PermManageGroups    Permission = "manage:groups"
	PermReadGroups      Permission = "read:groups"
	PermManageDevices   Permission = "manage:devices"
	PermReadCompliance  Permission = "read:compliance"
	PermManagePolicies  Permission = "manage:policies"
	PermManageMFA       Permission = "manage:mfa"
	PermManageAPITokens Permission = "manage:api-tokens"
	PermSelfService     Permission = "self:service"
	PermManageCampaigns Permission = "manage:campaigns"
	PermReviewCampaigns Permission = "review:campaigns"
	PermApproveRequests Permission = "approve:requests"
	PermSubmitApprovals    Permission = "submit:approvals"
	PermManageAccountPolicy Permission = "manage:account-policy"
	// PermManageOrgs is system-admin only: create/list organizations (tenants).
	PermManageOrgs Permission = "manage:orgs"
	// PermManageOrgMembers gates org-membership management. Unlike every other
	// permission here it is NOT decided purely from the JWT's global RBAC role
	// (RoleSuperAdmin, ...): an "org-admin" is an org-scoped role recorded per
	// membership in Postgres (org_memberships.role), orthogonal to the global
	// role. RequireOrgAdminOrSystemAdmin (below) is the actual gate used on
	// routes protected by this permission; it is listed here only so the
	// route-coverage guard test's allowlist stays honest about intent.
	PermManageOrgMembers Permission = "manage:org-members"
)

// permissionMatrix maps each permission to the roles that hold it.
var permissionMatrix = map[Permission][]Role{
	PermManageUsers:     {RoleSuperAdmin},
	PermOnboardOffboard: {RoleSuperAdmin},
	PermReadUsers:       {RoleSuperAdmin, RoleHelpdesk, RoleAuditor, RoleReadOnly},
	PermManageApps:      {RoleSuperAdmin},
	PermReadApps:        {RoleSuperAdmin, RoleHelpdesk, RoleAuditor, RoleReadOnly},
	PermReadAuditLogs:   {RoleSuperAdmin, RoleAuditor},
	PermExportAuditLogs: {RoleSuperAdmin, RoleAuditor},
	PermManageGroups:    {RoleSuperAdmin},
	PermReadGroups:      {RoleSuperAdmin, RoleHelpdesk, RoleAuditor, RoleReadOnly},
	PermManageDevices:   {RoleSuperAdmin, RoleHelpdesk},
	PermReadCompliance:  {RoleSuperAdmin, RoleHelpdesk, RoleAuditor, RoleReadOnly},
	PermManagePolicies:  {RoleSuperAdmin},
	PermManageMFA:       {RoleSuperAdmin, RoleHelpdesk},
	PermManageAPITokens: {RoleSuperAdmin},
	PermSelfService:     {RoleSuperAdmin, RoleHelpdesk, RoleAuditor, RoleReadOnly, RoleEndUser},
	PermManageCampaigns: {RoleSuperAdmin},
	PermReviewCampaigns: {RoleSuperAdmin, RoleAuditor},
	PermApproveRequests: {RoleSuperAdmin},
	PermSubmitApprovals:    {RoleSuperAdmin, RoleHelpdesk},
	PermManageAccountPolicy: {RoleSuperAdmin},
	PermManageOrgs:          {RoleSuperAdmin},
	// PermManageOrgMembers is NOT checked via this matrix — see
	// RequireOrgAdminOrSystemAdmin. Left unmapped (RoleSuperAdmin only) so any
	// accidental direct use of RequirePermission(PermManageOrgMembers) still
	// fails closed to system-admin rather than opening to everyone.
	PermManageOrgMembers: {RoleSuperAdmin},
}

// roleHasPermission checks whether a role holds a permission.
func roleHasPermission(role Role, perm Permission) bool {
	for _, r := range permissionMatrix[perm] {
		if r == role {
			return true
		}
	}
	return false
}

// RoleFromString parses a persisted/internal role string.
func RoleFromString(role string) (Role, bool) {
	switch Role(role) {
	case RoleSuperAdmin, RoleHelpdesk, RoleAuditor, RoleReadOnly, RoleEndUser:
		return Role(role), true
	default:
		return "", false
	}
}

// JWTClaims holds the claims we extract from the validated JWT.
type JWTClaims struct {
	Sub               string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	IsAdmin           bool   `json:"-"` // kept for back-compat; true when Role == RoleSuperAdmin
	Role              Role   `json:"-"` // resolved RBAC role
}

type claimsKeyType struct{}

var claimsKey = claimsKeyType{}

// HasPermission returns true when the claims in ctx hold the given permission.
// Returns false (deny) when ctx has no claims.
func HasPermission(ctx context.Context, perm Permission) bool {
	claims := GetClaims(ctx)
	if claims == nil {
		return false
	}
	return roleHasPermission(claims.Role, perm)
}

// RequirePermission returns a middleware that allows the request only if the
// authenticated actor holds the given permission. Fail-closed: no claims → 403.
func RequirePermission(perm Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !HasPermission(r.Context(), perm) {
				writeAuthError(w, http.StatusForbidden, "forbidden: insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolveRole maps a slice of Keycloak realm role names to our internal Role.
// Fail-closed: unknown roles → end-user. Ordered by privilege so the highest wins.
func resolveRole(roles []string) Role {
	roleSet := make(map[string]bool, len(roles))
	for _, r := range roles {
		roleSet[r] = true
	}
	switch {
	case roleSet["admin"] || roleSet["freecloud-admin"]:
		return RoleSuperAdmin
	case roleSet["freecloud-helpdesk"]:
		return RoleHelpdesk
	case roleSet["freecloud-auditor"]:
		return RoleAuditor
	case roleSet["freecloud-readonly"]:
		return RoleReadOnly
	default:
		return RoleEndUser
	}
}

// TokenDB is the minimal interface the API-token middleware needs for lookups.
type TokenDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// APITokenMiddleware wraps AuthMiddleware and also accepts fc_ prefixed API tokens.
// An API token has format "fc_<64 hex chars>"; only its SHA-256 hash is stored.
type APITokenMiddleware struct {
	*AuthMiddleware
	db TokenDB
}

// NewAPITokenMiddleware returns an APITokenMiddleware wrapping the given AuthMiddleware.
func NewAPITokenMiddleware(auth *AuthMiddleware, db TokenDB) *APITokenMiddleware {
	return &APITokenMiddleware{AuthMiddleware: auth, db: db}
}

// Middleware overrides AuthMiddleware.Middleware to also accept fc_ API tokens.
func (a *APITokenMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/health") {
			next.ServeHTTP(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			if strings.HasPrefix(tokenStr, "fc_") {
				a.handleAPIToken(w, r, next, tokenStr)
				return
			}
		}
		if strings.HasPrefix(authHeader, "fc_") {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized: missing Bearer token")
			return
		}
		// Fall through to standard JWT validation.
		a.AuthMiddleware.Middleware(next).ServeHTTP(w, r)
	})
}

func (a *APITokenMiddleware) handleAPIToken(w http.ResponseWriter, r *http.Request, next http.Handler, tokenStr string) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(tokenStr)))

	if a.db == nil {
		writeAuthError(w, http.StatusInternalServerError, "auth service temporarily unavailable")
		return
	}

	var id string
	var role string
	var serviceIdentity string
	var orgID string
	var revokedAt *time.Time
	var expiresAt *time.Time
	err := a.db.QueryRow(r.Context(),
		`SELECT id::TEXT, role, service_identity, org_id::TEXT, revoked_at, expires_at FROM api_tokens WHERE token_hash = $1`,
		hash,
	).Scan(&id, &role, &serviceIdentity, &orgID, &revokedAt, &expiresAt)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized: invalid API token")
		return
	}
	if revokedAt != nil {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized: token has been revoked")
		return
	}
	if expiresAt != nil && time.Now().After(*expiresAt) {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized: token has expired")
		return
	}

	resolved, ok := RoleFromString(role)
	if !ok {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized: invalid API token role")
		return
	}
	claims := &JWTClaims{
		Sub:               "api-token:" + id,
		PreferredUsername: "api-token:" + serviceIdentity,
		IsAdmin:           resolved == RoleSuperAdmin,
		Role:              resolved,
	}
	ctx := context.WithValue(r.Context(), claimsKey, claims)
	// C2/C5 (Epic C multi-tenant): an API token is scoped to the org it was
	// created in (api_tokens.org_id, Migration043) — NOT resolved via
	// org_memberships like a human JWT, since a token has no membership row.
	// Setting OrgContext directly here means OrgContextMiddleware's own
	// "already set" short-circuit takes over (see middleware/org.go), so a
	// super-admin-role token is correctly confined to its OWN org rather
	// than hitting the system-admin cross-org fallback that would otherwise
	// apply to any super-admin JWT with zero memberships.
	ctx = SetOrgContext(ctx, &OrgContext{OrgID: orgID, Role: OrgMembershipRoleAdmin})
	next.ServeHTTP(w, r.WithContext(ctx))
}

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
					jc := &JWTClaims{Role: RoleEndUser}
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
					// Extract realm roles and resolve RBAC role.
					if realmAccess, ok := claims["realm_access"].(map[string]interface{}); ok {
						if roles, ok := realmAccess["roles"].([]interface{}); ok {
							var roleStrs []string
							for _, r := range roles {
								if role, ok := r.(string); ok {
									roleStrs = append(roleStrs, role)
									if role == "admin" || role == "freecloud-admin" {
										jc.IsAdmin = true
									}
								}
							}
							jc.Role = resolveRole(roleStrs)
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
