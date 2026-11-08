package scim

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// BearerMiddleware returns middleware that enforces the legacy static SCIM
// bearer token (config.SCIMBearerToken). It is fail-closed: an empty token
// rejects ALL requests.
//
// C4 (Epic C multi-tenant): the legacy token authenticates on behalf of the
// Default Organization for backward compatibility with existing Okta/Entra
// integrations that predate multi-org support — see docs/adr/0005. It sets
// an OrgContext so every SCIM handler downstream can org-scope its query the
// same way whether the caller used the legacy path or a per-org token.
func BearerMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				respondError(w, http.StatusServiceUnavailable, "SCIM provisioning is not configured", "")
				return
			}
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				respondError(w, http.StatusUnauthorized, "Bearer token required", "")
				return
			}
			if !constantTimeStringEqual(strings.TrimPrefix(auth, "Bearer "), token) {
				respondError(w, http.StatusUnauthorized, "Invalid bearer token", "")
				return
			}
			ctx := middleware.SetOrgContext(r.Context(), &middleware.OrgContext{
				OrgID: middleware.DefaultOrgID, Role: middleware.OrgMembershipRoleAdmin,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OrgTokenDB is the minimal interface the org-scoped SCIM bearer
// middleware needs to look up a per-org token.
type OrgTokenDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// OrgBearerMiddleware authenticates the org-scoped SCIM base path
// (/scim/v2/orgs/{orgID}/...) against the scim_bearer_tokens table (C4,
// Migration043). The path's {orgID} must match the token's own org — a valid
// token for org A can never authenticate requests claiming to be for org B,
// which is exactly the cross-org SCIM-provisioning path this epic must not
// leave open.
func (s *Service) OrgBearerMiddleware(db OrgTokenDB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pathOrgID := chi.URLParam(r, "orgID")
			if err := s.host.ValidateUserID(pathOrgID); err != nil {
				respondError(w, http.StatusNotFound, "unknown organization", "")
				return
			}
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				respondError(w, http.StatusUnauthorized, "Bearer token required", "")
				return
			}
			tokenStr := strings.TrimPrefix(auth, "Bearer ")
			if db == nil {
				respondError(w, http.StatusServiceUnavailable, "SCIM provisioning is not configured", "")
				return
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(tokenStr)))
			var tokenOrgID string
			err := db.QueryRow(r.Context(),
				`SELECT org_id::TEXT FROM scim_bearer_tokens WHERE token_hash = $1 AND revoked_at IS NULL`,
				hash,
			).Scan(&tokenOrgID)
			if err != nil || tokenOrgID != pathOrgID {
				// Same 401 whether the token is unknown, revoked, or valid for a
				// DIFFERENT org — never leak which case it was to the caller.
				respondError(w, http.StatusUnauthorized, "Invalid bearer token", "")
				return
			}
			ctx := middleware.SetOrgContext(r.Context(), &middleware.OrgContext{
				OrgID: tokenOrgID, Role: middleware.OrgMembershipRoleAdmin,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// constantTimeStringEqual compares two strings in constant time (via their
// SHA-256 digests, so length differences do not short-circuit). Same
// construction as handlers' token comparison helper.
func constantTimeStringEqual(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}
