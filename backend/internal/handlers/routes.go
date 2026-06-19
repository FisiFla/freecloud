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

	// SCIM 2.0 provisioning — bearer-token authenticated, outside the user-JWT group.
	// SCIMBearerToken is injected by SetupSCIM (called from main after config load).
	r.Group(func(r chi.Router) {
		r.Use(h.scimBearerMW)
		r.Get("/scim/v2/Users", h.SCIMListUsers)
		r.Post("/scim/v2/Users", h.SCIMCreateUser)
		r.Get("/scim/v2/Users/{id}", h.SCIMGetUser)
		r.Patch("/scim/v2/Users/{id}", h.SCIMPatchUser)
		r.Delete("/scim/v2/Users/{id}", h.SCIMDeleteUser)
		// B1: SCIM Groups resource (RFC 7644)
		r.Get("/scim/v2/Groups", h.SCIMListGroups)
		r.Post("/scim/v2/Groups", h.SCIMCreateGroup)
		r.Get("/scim/v2/Groups/{id}", h.SCIMGetGroup)
		r.Patch("/scim/v2/Groups/{id}", h.SCIMPatchGroup)
		r.Delete("/scim/v2/Groups/{id}", h.SCIMDeleteGroup)
	})

	// A1: access evaluation — bearer-token authenticated, outside the user-JWT group.
	// Called by the Keycloak authenticator SPI (or any service) to gate SSO on posture.
	r.Group(func(r chi.Router) {
		r.Use(h.accessEvalBearerMW)
		r.Post("/api/v1/access/evaluate", h.EvaluateAccess)
	})

	// C3: self-service password reset — public, no JWT. Rate-limited to
	// prevent email flooding. Returns a fixed message to avoid user enumeration.
	forgotLimiter := middleware.NewRateLimiter(10, time.Minute)
	r.Group(func(r chi.Router) {
		r.Use(forgotLimiter.Middleware)
		r.Post("/api/v1/auth/forgot-password", h.ForgotPassword)
	})

	// Rate limiter for mutating endpoints: 20 requests / minute / client.
	mutateLimiter := middleware.NewRateLimiter(20, time.Minute)

	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.ActorIDMiddleware)

		// Sensitive write endpoints get the stricter rate limit.
		r.Group(func(r chi.Router) {
			r.Use(mutateLimiter.Middleware)
			r.Post("/api/v1/onboard", h.Onboard)
			r.Post("/api/v1/onboard/bulk", h.BulkOnboard)  // C1
			r.Post("/api/v1/offboard/{userId}", h.Offboard)
			r.Post("/api/v1/apps/create", h.CreateApp)
			r.Post("/api/v1/apps/{appId}/assign", h.AssignApp)

			// A4 — user profile update
			r.Patch("/api/v1/users/{id}", h.PatchUser)
			// A5 — admin password reset
			r.Post("/api/v1/users/{id}/reset-password", h.ResetPassword)

			// A3 — group management (admin-gated via isManagementEndpoint)
			r.Post("/api/v1/groups", h.CreateGroup)
			r.Post("/api/v1/users/{id}/groups", h.AssignUserToGroup)
			r.Delete("/api/v1/users/{id}/groups/{groupId}", h.UnassignUserFromGroup)
			r.Post("/api/v1/users/{id}/roles", h.AssignRealmRoleToUser)

			r.Post("/api/v1/users/{id}/require-mfa", h.RequireMFA)  // C2
			// B1: admin-only remote lock (distinct from wipe which runs in offboard)
			r.Post("/api/v1/devices/{id}/lock", h.RemoteLock)

			// A3: per-app access policy (admin-only write)
			r.Put("/api/v1/apps/{appId}/policy", h.UpsertAppAccessPolicy)

			// B2: Fleet team management (team-scoped MDM policies)
			r.Post("/api/v1/teams", h.CreateTeam)
			r.Post("/api/v1/teams/{id}/policies", h.AssignTeamPolicy)
			r.Post("/api/v1/teams/{id}/hosts", h.MoveHostToTeam)
		})

		r.Post("/api/v1/auth/device-check", h.DeviceCheck)
		r.Get("/api/v1/apps", h.ListApps)
		// A3: per-app access policy (read)
		r.Get("/api/v1/apps/{appId}/policy", h.GetAppAccessPolicy)
		r.Get("/api/v1/audit-logs", h.ListAuditLogs)
		r.Get("/api/v1/audit-logs/export", h.ExportAuditLogs)  // C4
		r.Get("/api/v1/users", h.ListUsers)
		r.Get("/api/v1/users/{id}", h.GetUser)

		// A3 — read-only group/role endpoints (admin-gated via isManagementEndpoint)
		r.Get("/api/v1/groups", h.ListGroups)
		r.Get("/api/v1/roles", h.ListRealmRoles)

		r.Get("/api/v1/users/{id}/mfa-status", h.GetMFAStatus)  // C2
		// B2: software inventory for a user's devices
		r.Get("/api/v1/users/{id}/devices/software", h.GetDeviceSoftware)
		// B3: compliance posture
		r.Get("/api/v1/users/{id}/devices/compliance", h.GetUserCompliance)
		r.Get("/api/v1/compliance", h.GetOrgCompliance)
		// B2: list policies and teams (read-only, no mutation gate)
		r.Get("/api/v1/policies", h.ListPolicies)
		r.Get("/api/v1/teams", h.ListTeams)

		// Admin: drift / reconciliation report (read-only).
		r.Get("/api/v1/admin/drift", h.GetDrift)
	})
}
