package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// fakeAuthMW injects valid admin claims and the actor ID into the context,
// bypassing real JWT verification. Used only by router-level tests.
func fakeAuthMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := &middleware.JWTClaims{
			Sub:               "test-admin",
			PreferredUsername: "admin",
			Email:             "admin@test.local",
			IsAdmin:           true,
		}
		ctx := middleware.SetClaims(r.Context(), claims)
		ctx = context.WithValue(ctx, middleware.ActorIDKey, "admin")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// newTestRouter builds a chi router wired exactly like the real server
// (auth + actor + rate-limited mutating endpoints) but with a fake auth MW.
func newTestRouter(h *Handler) *chi.Mux {
	r := chi.NewRouter()
	SetupRoutes(r, h, fakeAuthMW)
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
