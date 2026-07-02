package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// testLimiterFactory builds in-memory limiters, matching the dev/test default
// selected by main.go when REDIS_URL is unset. Router-level tests exercise
// the rate-limit wiring itself, not the Redis-vs-memory choice (that is
// covered by middleware/ratelimit_redis_test.go).
func testLimiterFactory(limit int, window time.Duration, _ string) middleware.Limiter {
	return middleware.NewRateLimiter(limit, window)
}

// fakeRoleAuthMW injects valid claims, the actor ID, and a resolved org
// context into the request, bypassing real JWT verification and DB-backed
// org-membership lookups. Used only by router-level tests. The org membership
// role mirrors the global role for test simplicity: RoleSuperAdmin resolves
// as org-admin (system-admins can act on any org); every other role resolves
// as a plain "member" so org-admin-gated routes correctly deny them, matching
// how a real end-user/helpdesk/etc. would have no org_memberships.role =
// 'org-admin' row unless explicitly granted one.
func fakeRoleAuthMW(role middleware.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &middleware.JWTClaims{
				Sub:               "test-user",
				PreferredUsername: "test-user",
				Email:             "user@test.local",
				IsAdmin:           role == middleware.RoleSuperAdmin,
				Role:              role,
			}
			orgRole := "member"
			if role == middleware.RoleSuperAdmin {
				orgRole = middleware.OrgMembershipRoleAdmin
			}
			ctx := middleware.SetClaims(r.Context(), claims)
			ctx = context.WithValue(ctx, middleware.ActorIDKey, "test-user")
			ctx = middleware.SetOrgContext(ctx, &middleware.OrgContext{
				OrgID: middleware.DefaultOrgID,
				Role:  orgRole,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func fakeAuthMW(next http.Handler) http.Handler {
	return fakeRoleAuthMW(middleware.RoleSuperAdmin)(next)
}

// newTestRouter builds a chi router wired exactly like the real server
// (auth + actor + rate-limited mutating endpoints) but with a fake auth MW.
func newTestRouter(h *Handler) *chi.Mux {
	r := chi.NewRouter()
	SetupRoutes(r, h, fakeAuthMW, testLimiterFactory)
	return r
}

func newRoleTestRouter(h *Handler, role middleware.Role) *chi.Mux {
	r := chi.NewRouter()
	SetupRoutes(r, h, fakeRoleAuthMW(role), testLimiterFactory)
	return r
}

// TestRouterOnboardRateLimited proves the mutating-endpoint limiter is wired
// at the router level: 20 POSTs to /onboard succeed, the 21st is 429.
func TestRouterOnboardRateLimited(t *testing.T) {
	h := setupTestHandler(t)
	r := newTestRouter(h)

	body := `{"firstName":"A","lastName":"B","email":"a@b.com"}`
	for i := 1; i <= 20; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", strings.NewReader(body))
		req.RemoteAddr = "10.0.0.99:5000"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d (body: %s)", i, rec.Code, rec.Body.String())
		}
	}

	// 21st from the same client must be rate-limited.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.99:5001"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit onboard: expected 429, got %d", rec.Code)
	}
}

func TestRouterReadOnlyCanReadUsersButCannotOnboard(t *testing.T) {
	h := setupTestHandler(t)
	r := newRoleTestRouter(h, middleware.RoleReadOnly)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-only GET /users: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	body := `{"firstName":"A","lastName":"B","email":"a@b.com"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/onboard", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-only POST /onboard: expected 403, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRouterHelpdeskCanSubmitApprovalButCannotDirectlyOnboard(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			if strings.Contains(sql, "INSERT INTO approval_requests") {
				return fakeRow{scanFn: func(dest ...any) error {
					*(dest[0].(*string)) = "00000000-0000-0000-0000-000000000001"
					return nil
				}}
			}
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		},
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := newRoleTestRouter(h, middleware.RoleHelpdesk)

	body := `{"firstName":"A","lastName":"B","email":"a@b.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("helpdesk POST /onboard: expected 403, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	approvalBody := `{"actionType":"onboard","payload":{"firstName":"A","lastName":"B","email":"a@b.com"}}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/approval-requests", strings.NewReader(approvalBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("helpdesk POST /approval-requests: expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	appBody := `{"name":"App","protocol":"OIDC","redirectURIs":["https://app.example/cb"],"baseURL":"https://app.example"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/apps/create", strings.NewReader(appBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("helpdesk POST /apps/create: expected 403, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestRouterReadEndpointsNotRateLimited proves read endpoints (which are
// outside the mutating group) are not subject to the write limiter, even
// after the onboard bucket is exhausted.
func TestRouterReadEndpointsNotRateLimited(t *testing.T) {
	h := setupTestHandler(t)
	r := newTestRouter(h)

	// Exhaust the onboard limiter first (same client).
	body := `{"firstName":"A","lastName":"B","email":"a@b.com"}`
	for i := 0; i < 25; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", strings.NewReader(body))
		req.RemoteAddr = "10.0.0.98:5000"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}

	// GET /users from the same client must still succeed — read endpoints
	// are not behind the mutate limiter.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.RemoteAddr = "10.0.0.98:5001"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("read endpoint after onboard limit: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestEveryAPIRouteIsPermissionGated is a guard for the per-route RBAC model.
// When the central path-prefix admin gate was removed, authorization became
// opt-in per route via RequirePermission(...). The risk is that a future route
// is added without a gate and is silently authenticated-but-unauthorized.
//
// This test walks the entire route tree and asserts that a minimal end-user
// token (which holds only PermSelfService) is forbidden (403) from every route
// EXCEPT an explicit allowlist of intentional exceptions: public probes,
// dedicated-bearer service surfaces (which carry their own auth), and the
// self-service routes an end-user legitimately reaches. Adding a sensitive
// route without gating it — or without consciously allowlisting it — fails here.
func TestEveryAPIRouteIsPermissionGated(t *testing.T) {
	h := setupTestHandler(t)
	r := newRoleTestRouter(h, middleware.RoleEndUser)

	// Routes intentionally NOT behind a management/read RBAC permission.
	allow := map[string]bool{
		// Public liveness/readiness/health probes.
		"GET /healthz":                true,
		"GET /readyz":                 true,
		"GET /api/v1/health":          true,
		"GET /api/v1/health/keycloak": true,
		"GET /api/v1/health/fleetdm":  true,
		// Public / service-authenticated (own auth, not RBAC).
		"POST /api/v1/fleet/enrollment-callback":  true, // HMAC
		"POST /api/v1/auth/forgot-password":       true, // public, rate-limited
		"POST /api/v1/access/evaluate":            true, // dedicated bearer
		"POST /api/v1/enrollment/device-identity": true, // A3: enrollment cookie, rate-limited, no JWT
		// SCIM discovery — unauthenticated per RFC 7644 §2.
		"GET /scim/v2/ServiceProviderConfig": true,
		"GET /scim/v2/ResourceTypes":         true,
		"GET /scim/v2/Schemas":               true,
		// SCIM provisioning surface — dedicated bearer token.
		"GET /scim/v2/Users":          true,
		"POST /scim/v2/Users":         true,
		"GET /scim/v2/Users/{id}":     true,
		"PATCH /scim/v2/Users/{id}":   true,
		"DELETE /scim/v2/Users/{id}":  true,
		"GET /scim/v2/Groups":         true,
		"POST /scim/v2/Groups":        true,
		"GET /scim/v2/Groups/{id}":    true,
		"PATCH /scim/v2/Groups/{id}":  true,
		"DELETE /scim/v2/Groups/{id}": true,
		// C4: org-scoped SCIM surface — dedicated per-org bearer token
		// (SCIMOrgBearerMiddleware), same auth class as the legacy path above.
		"GET /scim/v2/orgs/{orgID}/Users":          true,
		"POST /scim/v2/orgs/{orgID}/Users":         true,
		"GET /scim/v2/orgs/{orgID}/Users/{id}":     true,
		"PATCH /scim/v2/orgs/{orgID}/Users/{id}":   true,
		"DELETE /scim/v2/orgs/{orgID}/Users/{id}":  true,
		"GET /scim/v2/orgs/{orgID}/Groups":         true,
		"POST /scim/v2/orgs/{orgID}/Groups":        true,
		"GET /scim/v2/orgs/{orgID}/Groups/{id}":    true,
		"PATCH /scim/v2/orgs/{orgID}/Groups/{id}":  true,
		"DELETE /scim/v2/orgs/{orgID}/Groups/{id}": true,
		// Test-only enrollment-token helper — SCIM-bearer-authenticated (APP_ENV=test only).
		"POST /api/v1/e2e/enrollment-token": true,
		// Self-service — gated by PermSelfService, which end-user holds.
		"POST /api/v1/auth/device-check": true,
		"GET /api/v1/portal/me/devices":                    true,
		"GET /api/v1/portal/me/apps":                       true,
		"GET /api/v1/portal/me/compliance":                 true,
		"POST /api/v1/portal/access-requests":              true,
		// C1/C3 (Epic C multi-tenant): every authenticated user can see their
		// own identity, global role, and org memberships — gated by
		// PermSelfService, which end-user holds.
		"GET /api/v1/me": true,
		// B1: MFA self-service — gated by PermSelfService, which end-user holds.
		"GET /api/v1/portal/me/mfa/factors":                true,
		"POST /api/v1/portal/me/mfa/totp/enroll":           true,
		"POST /api/v1/portal/me/mfa/webauthn/enroll":       true,
		"DELETE /api/v1/portal/me/mfa/factors/{credId}":    true,
		"GET /api/v1/portal/me/recovery-codes":             true,
		"POST /api/v1/portal/me/recovery-codes":            true,
		// B1 (setup wizard): unauthenticated, fail-closed once provisioned.
		"GET /api/v1/setup/status": true,
		"POST /api/v1/setup":       true,
	}

	paramRe := regexp.MustCompile(`\{[^}]*\}`)
	var routeCount int
	var ungated []string

	walkErr := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routeCount++
		key := method + " " + route
		// Unique client per request so no rate limiter ever interferes.
		req := httptest.NewRequest(method, paramRe.ReplaceAllString(route, "x"), nil)
		req.RemoteAddr = fmt.Sprintf("10.10.%d.%d:1000", routeCount/256, routeCount%256)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if allow[key] {
			// Allowlisted routes are public / bearer-authed / self-service, so
			// an end-user must never be RBAC-forbidden on them. A 403 here means
			// a gate was tightened without updating this allowlist.
			if rec.Code == http.StatusForbidden {
				t.Errorf("allowlisted route %q returned 403 to end-user — its gate changed? body=%s",
					key, strings.TrimSpace(rec.Body.String()))
			}
			return nil
		}
		// Everything else must be RBAC-gated: a minimal end-user gets 403.
		if rec.Code != http.StatusForbidden {
			ungated = append(ungated, fmt.Sprintf("%s -> %d", key, rec.Code))
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("chi.Walk failed: %v", walkErr)
	}
	if routeCount < 30 {
		t.Fatalf("route walk found only %d routes; the walk is likely broken", routeCount)
	}
	if len(ungated) > 0 {
		t.Fatalf("these routes are NOT permission-gated (a minimal end-user token did not get 403).\n"+
			"Wrap each with middleware.RequirePermission(...) in routes.go, or add it to the allowlist "+
			"if it is intentionally public/self-service:\n  %s", strings.Join(ungated, "\n  "))
	}
}

// zeroMembershipDB is a full DBPool (so it can back a real *Handler) that
// reports no memberships/organizations/rows for any lookup. It lets
// OrgContextMiddleware's real DB-backed resolution path execute (the
// loadMemberships query it always runs before validating X-Org-Id) without
// needing a live Postgres connection.
type zeroMembershipDB struct{}

func (zeroMembershipDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
}
func (zeroMembershipDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &fakeQueryRows{rows: nil}, nil
}
func (zeroMembershipDB) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (zeroMembershipDB) Begin(_ context.Context) (pgx.Tx, error) {
	return nil, errors.New("zeroMembershipDB.Begin not implemented")
}

// authOnlyMW sets JWT claims (as AuthMiddleware would after verifying a real
// token) but does NOT pre-resolve an OrgContext, so the real
// middleware.OrgContextMiddleware wired in routes.go actually runs — unlike
// fakeRoleAuthMW (used by newRoleTestRouter), which pre-sets an OrgContext
// and thereby causes OrgContextMiddleware to skip its own resolution logic
// entirely (see the "already set" short-circuit in org.go). Using
// fakeRoleAuthMW here would make this test vacuously pass regardless of
// whether OrgContextMiddleware is even wired.
func authOnlyMW(role middleware.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := &middleware.JWTClaims{
				Sub:               "test-user-no-preset-org",
				PreferredUsername: "test-user-no-preset-org",
				Email:             "user@test.local",
				IsAdmin:           role == middleware.RoleSuperAdmin,
				Role:              role,
			}
			ctx := middleware.SetClaims(r.Context(), claims)
			ctx = context.WithValue(ctx, middleware.ActorIDKey, "test-user-no-preset-org")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// TestEveryTenantScopedRouteResolvesOrgContext is the C2-mandated extension of
// the route-coverage guard: every tenant-scoped route must pass through
// OrgContextMiddleware, not just RequirePermission(...). A route that skips
// org resolution entirely is exactly the cross-org-leak vulnerability class
// this epic must not create.
//
// Technique: send a malformed X-Org-Id header ("not-a-uuid"). Only
// OrgContextMiddleware validates that header's shape (isValidOrgID) — no
// handler duplicates that check — so a route wired through it MUST reject
// with 403 regardless of which permission/role gate sits in front of it.
// A route that responds with anything else (200, a handler-specific 400/404,
// a 5xx from treating the garbage string as a real ID, ...) proves the
// header — and therefore the whole org-resolution step — was never
// consulted for that route.
//
// This is deliberately independent of TestEveryAPIRouteIsPermissionGated:
// that test proves permission gates exist; this one proves org resolution
// sits in the SAME chain as those gates, by using a super-admin identity
// (so every permission gate passes) and letting a bad X-Org-Id be the only
// possible source of rejection.
func TestEveryTenantScopedRouteResolvesOrgContext(t *testing.T) {
	h := NewHandler(zeroMembershipDB{}, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	r := chi.NewRouter()
	SetupRoutes(r, h, authOnlyMW(middleware.RoleSuperAdmin))

	// Routes intentionally outside the org-scoped authenticated group: public
	// probes, dedicated-bearer service surfaces (own auth, no X-Org-Id
	// concept), and unauthenticated setup/discovery endpoints. Mirrors the
	// "public/self-authenticating" half of TestEveryAPIRouteIsPermissionGated's
	// allowlist — these never reach OrgContextMiddleware by design, so a
	// garbage X-Org-Id header is inert for them.
	exempt := map[string]bool{
		"GET /healthz": true, "GET /readyz": true, "GET /api/v1/health": true,
		"GET /api/v1/health/keycloak": true, "GET /api/v1/health/fleetdm": true,
		"POST /api/v1/fleet/enrollment-callback":  true,
		"POST /api/v1/auth/forgot-password":       true,
		"POST /api/v1/access/evaluate":            true,
		"POST /api/v1/enrollment/device-identity": true,
		"GET /scim/v2/ServiceProviderConfig":       true,
		"GET /scim/v2/ResourceTypes":               true,
		"GET /scim/v2/Schemas":                     true,
		"GET /scim/v2/Users": true, "POST /scim/v2/Users": true,
		"GET /scim/v2/Users/{id}": true, "PATCH /scim/v2/Users/{id}": true, "DELETE /scim/v2/Users/{id}": true,
		"GET /scim/v2/Groups": true, "POST /scim/v2/Groups": true,
		"GET /scim/v2/Groups/{id}": true, "PATCH /scim/v2/Groups/{id}": true, "DELETE /scim/v2/Groups/{id}": true,
		"POST /api/v1/e2e/enrollment-token": true,
		"GET /api/v1/setup/status":          true,
		"POST /api/v1/setup":                true,
		// C4: org-scoped SCIM surface authenticates via SCIMOrgBearerMiddleware
		// against the {orgID} path param directly (not X-Org-Id / claims-based
		// OrgContextMiddleware resolution) — a malformed X-Org-Id header is
		// inert here by design; the middleware validates {orgID} instead
		// (fail-closed 404 for a non-UUID org segment, proven by
		// TestSCIMOrgBearerMiddleware in scim_test.go).
		"GET /scim/v2/orgs/{orgID}/Users": true, "POST /scim/v2/orgs/{orgID}/Users": true,
		"GET /scim/v2/orgs/{orgID}/Users/{id}": true, "PATCH /scim/v2/orgs/{orgID}/Users/{id}": true, "DELETE /scim/v2/orgs/{orgID}/Users/{id}": true,
		"GET /scim/v2/orgs/{orgID}/Groups": true, "POST /scim/v2/orgs/{orgID}/Groups": true,
		"GET /scim/v2/orgs/{orgID}/Groups/{id}": true, "PATCH /scim/v2/orgs/{orgID}/Groups/{id}": true, "DELETE /scim/v2/orgs/{orgID}/Groups/{id}": true,
	}

	paramRe := regexp.MustCompile(`\{[^}]*\}`)
	var routeCount int
	var bypassed []string

	walkErr := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routeCount++
		key := method + " " + route
		if exempt[key] {
			return nil
		}
		req := httptest.NewRequest(method, paramRe.ReplaceAllString(route, "x"), nil)
		req.Header.Set("X-Org-Id", "not-a-uuid")
		req.RemoteAddr = fmt.Sprintf("10.12.%d.%d:1000", routeCount/256, routeCount%256)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			bypassed = append(bypassed, fmt.Sprintf("%s -> %d (body: %s)", key, rec.Code, strings.TrimSpace(rec.Body.String())))
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("chi.Walk failed: %v", walkErr)
	}
	if routeCount < 30 {
		t.Fatalf("route walk found only %d routes; the walk is likely broken", routeCount)
	}
	if len(bypassed) > 0 {
		t.Fatalf("these tenant-scoped routes did NOT reject a malformed X-Org-Id with 403, meaning "+
			"OrgContextMiddleware is not in their chain (a super-admin identity passes every permission "+
			"gate, so anything other than 403 here means org resolution was skipped):\n  %s",
			strings.Join(bypassed, "\n  "))
	}
}
