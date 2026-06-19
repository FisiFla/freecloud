package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// SetupRoutes registers all API routes on the provided chi router.
// authMW is applied to all /api/v1 routes except /health.
// A per-client rate limit protects the sensitive onboarding/offboarding
// endpoints from abuse.
func SetupRoutes(r chi.Router, h *Handler, authMW func(http.Handler) http.Handler) {
	// Liveness/readiness probes (k8s-style) and the simple health endpoint.
	r.Get("/healthz", h.Healthz)
	r.Get("/readyz", h.Readyz)
	r.Get("/api/v1/health", h.Health)

	// The dependency health checks ping Keycloak/Fleet, so rate-limit them to
	// prevent unauthenticated amplification against those upstreams.
	healthLimiter := middleware.NewRateLimiter(30, time.Minute)
	r.Group(func(r chi.Router) {
		r.Use(healthLimiter.Middleware)
		r.Get("/api/v1/health/keycloak", h.HealthKeycloak)
		r.Get("/api/v1/health/fleetdm", h.HealthFleet)
	})

	// FleetDM enrollment callback — called by Fleet (not a browser) and
	// authenticated by an HMAC signature over the body, so it sits OUTSIDE the
	// JWT auth group.
	r.Post("/api/v1/fleet/enrollment-callback", h.FleetEnrollmentCallback)

	// Rate limiter for mutating endpoints: 20 requests / minute / client.
	mutateLimiter := middleware.NewRateLimiter(20, time.Minute)

	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.ActorIDMiddleware)

		// Sensitive write endpoints get the stricter rate limit.
		r.Group(func(r chi.Router) {
			r.Use(mutateLimiter.Middleware)
			r.Post("/api/v1/onboard", h.Onboard)
			r.Post("/api/v1/offboard/{userId}", h.Offboard)
			r.Post("/api/v1/apps/create", h.CreateApp)
			r.Post("/api/v1/apps/{appId}/assign", h.AssignApp)
		})

		r.Post("/api/v1/auth/device-check", h.DeviceCheck)
		r.Get("/api/v1/apps", h.ListApps)
		r.Get("/api/v1/audit-logs", h.ListAuditLogs)
		r.Get("/api/v1/users", h.ListUsers)
		r.Get("/api/v1/users/{id}", h.GetUser)

		// Admin: drift / reconciliation report (read-only).
		r.Get("/api/v1/admin/drift", h.GetDrift)
	})
}
