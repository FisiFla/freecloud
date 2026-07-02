package middleware

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// DefaultOrgID is the fixed UUID of the "Default Organization" seeded by
// Migration043. Every pre-v1.7 install's data is backfilled to this org, and
// legacy service-to-service tokens (SCIM bearer, access-eval) that carry no
// org context map to it for backward compatibility.
const DefaultOrgID = "00000000-0000-0000-0000-000000000001"

// orgKeyType is a private context-key type to avoid collisions.
type orgKeyType struct{}

var orgKey = orgKeyType{}

// OrgDB is the minimal interface the org-context middleware needs to resolve
// a caller's memberships.
type OrgDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// OrgContext holds the resolved active org for a request plus enough of the
// caller's membership to authorize org-scoped actions without a second query.
type OrgContext struct {
	// OrgID is the active organization for this request. Every tenant-scoped
	// query/handler must filter on this value.
	OrgID string
	// Role is the caller's membership role within OrgID: "org-admin" or
	// "member". Empty for system-admin callers acting via the system-admin
	// bypass (see GetOrgContext callers that also check RoleSuperAdmin).
	Role string
}

// GetOrgContext retrieves the resolved OrgContext from the request context.
// Returns nil when no org context was resolved (the middleware always sets
// one on success, so nil here means the request should have already been
// rejected — treat a nil OrgContext as "deny").
func GetOrgContext(ctx context.Context) *OrgContext {
	if ctx == nil {
		return nil
	}
	if oc, ok := ctx.Value(orgKey).(*OrgContext); ok {
		return oc
	}
	return nil
}

// SetOrgContext stores an OrgContext in the context. Exposed for tests and
// for the SCIM/access-eval bearer paths (C4/C5) which resolve org membership
// from a token rather than from OrgContextMiddleware.
func SetOrgContext(ctx context.Context, oc *OrgContext) context.Context {
	return context.WithValue(ctx, orgKey, oc)
}

// writeOrgError writes a fail-closed JSON 403 for org-resolution failures.
// Deliberately mirrors writeAuthError's shape ({"success":false,"error":...})
// so frontend error handling is uniform.
func writeOrgError(w http.ResponseWriter, status int, message string) {
	writeAuthError(w, status, message)
}

// OrgContextMiddleware resolves the active organization for an authenticated
// request and stores it in the request context for downstream handlers.
//
// Resolution order (fail-closed at every step — no implicit cross-org fallback):
//  1. If the request carries X-Org-Id, it MUST be a UUID matching one of the
//     caller's memberships (or the caller must hold the system-admin role,
//     which may act on any org). Anything else is 403.
//  2. Otherwise, the caller must have exactly one membership; that becomes
//     the active org. Zero memberships or more than one (ambiguous, no
//     explicit header) is 403 — never guess.
//
// System-admin (RoleSuperAdmin) callers with no memberships and no
// X-Org-Id are defaulted to DefaultOrgID so existing system-admin-only
// surfaces (e.g. the pre-v1.7 UI while the org switcher rolls out) keep
// working; they can still target any org explicitly via X-Org-Id.
func OrgContextMiddleware(db OrgDB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeOrgError(w, http.StatusForbidden, "forbidden: no authenticated identity for org resolution")
				return
			}

			// Service-to-service callers (API tokens, SCIM/access-eval bearer)
			// authenticate via their own dedicated middleware which sets an
			// OrgContext directly (see api_tokens.go / scim.go / access_eval.go).
			// If one is already set, respect it and skip membership resolution.
			if oc := GetOrgContext(r.Context()); oc != nil {
				next.ServeHTTP(w, r.WithContext(r.Context()))
				return
			}

			if db == nil {
				writeOrgError(w, http.StatusForbidden, "forbidden: org resolution unavailable")
				return
			}

			ctx := r.Context()
			userID := claims.Sub

			requestedOrg := r.Header.Get("X-Org-Id")

			memberships, err := loadMemberships(ctx, db, userID)
			if err != nil {
				writeOrgError(w, http.StatusForbidden, "forbidden: failed to resolve org membership")
				return
			}

			isSystemAdmin := claims.Role == RoleSuperAdmin

			if requestedOrg != "" {
				if !isValidOrgID(requestedOrg) {
					writeOrgError(w, http.StatusForbidden, "forbidden: invalid X-Org-Id")
					return
				}
				if role, ok := memberships[requestedOrg]; ok {
					ctx = SetOrgContext(ctx, &OrgContext{OrgID: requestedOrg, Role: role})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				if isSystemAdmin {
					// System-admin may act on any org, even without an explicit
					// membership row — but the org must actually exist.
					exists, err := orgExists(ctx, db, requestedOrg)
					if err != nil || !exists {
						writeOrgError(w, http.StatusForbidden, "forbidden: unknown X-Org-Id")
						return
					}
					ctx = SetOrgContext(ctx, &OrgContext{OrgID: requestedOrg, Role: OrgMembershipRoleAdmin})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				writeOrgError(w, http.StatusForbidden, "forbidden: not a member of the requested organization")
				return
			}

			// No explicit header: fall back to the caller's sole membership.
			if len(memberships) == 1 {
				for orgID, role := range memberships {
					ctx = SetOrgContext(ctx, &OrgContext{OrgID: orgID, Role: role})
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
			if len(memberships) > 1 {
				writeOrgError(w, http.StatusForbidden, "forbidden: multiple organizations available; specify X-Org-Id")
				return
			}

			// Zero memberships: system-admin defaults to the Default Organization
			// (keeps pre-org-switcher UI flows working); anyone else is denied.
			if isSystemAdmin {
				ctx = SetOrgContext(ctx, &OrgContext{OrgID: DefaultOrgID, Role: OrgMembershipRoleAdmin})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			writeOrgError(w, http.StatusForbidden, "forbidden: no organization membership")
		})
	}
}

// loadMemberships returns a map of org_id -> role for the given user.
func loadMemberships(ctx context.Context, db OrgDB, userID string) (map[string]string, error) {
	memberships := make(map[string]string)
	rows, err := db.Query(ctx,
		`SELECT org_id::TEXT, role FROM org_memberships WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var orgID, role string
		if err := rows.Scan(&orgID, &role); err != nil {
			return nil, err
		}
		memberships[orgID] = role
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return memberships, nil
}

// orgExists checks whether an organization row exists (used for the
// system-admin any-org override path).
func orgExists(ctx context.Context, db OrgDB, orgID string) (bool, error) {
	var found string
	err := db.QueryRow(ctx, `SELECT id::TEXT FROM organizations WHERE id = $1`, orgID).Scan(&found)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RequireOrgAdminOrSystemAdmin gates a route to callers who are either a
// SYSTEM admin (cross-org, RoleSuperAdmin) or the org-admin of their
// currently-resolved organization (OrgContext.Role == OrgMembershipRoleAdmin).
// Must run after AuthMiddleware + OrgContextMiddleware. Fail-closed: missing
// claims or org context denies.
func RequireOrgAdminOrSystemAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims != nil && claims.Role == RoleSuperAdmin {
			next.ServeHTTP(w, r)
			return
		}
		oc := GetOrgContext(r.Context())
		if oc != nil && oc.Role == OrgMembershipRoleAdmin {
			next.ServeHTTP(w, r)
			return
		}
		writeOrgError(w, http.StatusForbidden, "forbidden: requires org-admin or system-admin")
	})
}

// isValidOrgID reports whether s looks like a well-formed UUID. Reuses the
// same pattern as handlers.isValidUUID but duplicated here to avoid an
// import cycle (handlers already imports middleware).
func isValidOrgID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}
