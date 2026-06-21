package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// fakeRoleAuthMW injects valid claims and the actor ID into the context,
// bypassing real JWT verification. Used only by router-level tests.
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
			ctx := middleware.SetClaims(r.Context(), claims)
			ctx = context.WithValue(ctx, middleware.ActorIDKey, "test-user")
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
	SetupRoutes(r, h, fakeAuthMW)
	return r
}

func newRoleTestRouter(h *Handler, role middleware.Role) *chi.Mux {
	r := chi.NewRouter()
	SetupRoutes(r, h, fakeRoleAuthMW(role))
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

func TestRouterHelpdeskCanOnboardButCannotCreateApp(t *testing.T) {
	h := setupTestHandler(t)
	r := newRoleTestRouter(h, middleware.RoleHelpdesk)

	body := `{"firstName":"A","lastName":"B","email":"a@b.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboard", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("helpdesk POST /onboard: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
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
		"POST /api/v1/fleet/enrollment-callback": true, // HMAC
		"POST /api/v1/auth/forgot-password":      true, // public, rate-limited
		"POST /api/v1/access/evaluate":           true, // dedicated bearer
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
		// Self-service — gated by PermSelfService, which end-user holds.
		"POST /api/v1/auth/device-check":      true,
		"GET /api/v1/portal/me/devices":       true,
		"GET /api/v1/portal/me/apps":          true,
		"GET /api/v1/portal/me/compliance":    true,
		"POST /api/v1/portal/access-requests": true,
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
