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

	// A3 (FCEX3-7): Device-identity cookie — browser-facing, unauthenticated.
	// Called after Fleet enrollment to set the freecloud-device-id cookie that
	// the Keycloak SPI reads during login.  Rate-limited to 20 req/min.
	deviceCookieLimiter := middleware.NewRateLimiter(20, time.Minute)
	r.Group(func(r chi.Router) {
		r.Use(deviceCookieLimiter.Middleware)
		r.Post("/api/v1/enrollment/device-identity", h.SetDeviceIdentityCookie)
	})

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
			r.With(middleware.RequirePermission(middleware.PermOnboardOffboard)).Post("/api/v1/onboard", h.Onboard)
			r.With(middleware.RequirePermission(middleware.PermOnboardOffboard)).Post("/api/v1/onboard/bulk", h.BulkOnboard) // C1
			r.With(middleware.RequirePermission(middleware.PermOnboardOffboard)).Post("/api/v1/offboard/{userId}", h.Offboard)
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Post("/api/v1/apps/create", h.CreateApp)
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Post("/api/v1/apps/{appId}/assign", h.AssignApp)

			// A4 — user profile update
			r.With(middleware.RequirePermission(middleware.PermManageUsers)).Patch("/api/v1/users/{id}", h.PatchUser)
			// A5 — admin password reset
			r.With(middleware.RequirePermission(middleware.PermManageUsers)).Post("/api/v1/users/{id}/reset-password", h.ResetPassword)

			// A3 — group and role management
			r.With(middleware.RequirePermission(middleware.PermManageGroups)).Post("/api/v1/groups", h.CreateGroup)
			r.With(middleware.RequirePermission(middleware.PermManageGroups)).Post("/api/v1/users/{id}/groups", h.AssignUserToGroup)
			r.With(middleware.RequirePermission(middleware.PermManageGroups)).Delete("/api/v1/users/{id}/groups/{groupId}", h.UnassignUserFromGroup)
			r.With(middleware.RequirePermission(middleware.PermManageUsers)).Post("/api/v1/users/{id}/roles", h.AssignRealmRoleToUser)

			r.With(middleware.RequirePermission(middleware.PermManageMFA)).Post("/api/v1/users/{id}/require-mfa", h.RequireMFA) // C2
			// B1: remote lock (distinct from wipe which runs in offboard)
			r.With(middleware.RequirePermission(middleware.PermManageDevices)).Post("/api/v1/devices/{id}/lock", h.RemoteLock)

			// A3: per-app access policy write
			r.With(middleware.RequirePermission(middleware.PermManagePolicies)).Put("/api/v1/apps/{appId}/policy", h.UpsertAppAccessPolicy)

			// B2: Fleet team management (team-scoped MDM policies)
			r.With(middleware.RequirePermission(middleware.PermManagePolicies)).Post("/api/v1/teams", h.CreateTeam)
			r.With(middleware.RequirePermission(middleware.PermManagePolicies)).Post("/api/v1/teams/{id}/policies", h.AssignTeamPolicy)
			r.With(middleware.RequirePermission(middleware.PermManageDevices)).Post("/api/v1/teams/{id}/hosts", h.MoveHostToTeam)
		})

		r.With(middleware.RequirePermission(middleware.PermSelfService)).Post("/api/v1/auth/device-check", h.DeviceCheck)
		r.With(middleware.RequirePermission(middleware.PermReadApps)).Get("/api/v1/apps", h.ListApps)
		// A3: per-app access policy read
		r.With(middleware.RequirePermission(middleware.PermReadApps)).Get("/api/v1/apps/{appId}/policy", h.GetAppAccessPolicy)
		r.With(middleware.RequirePermission(middleware.PermReadAuditLogs)).Get("/api/v1/audit-logs", h.ListAuditLogs)
		r.With(middleware.RequirePermission(middleware.PermExportAuditLogs)).Get("/api/v1/audit-logs/export", h.ExportAuditLogs) // C4
		r.With(middleware.RequirePermission(middleware.PermReadUsers)).Get("/api/v1/users", h.ListUsers)
		r.With(middleware.RequirePermission(middleware.PermReadUsers)).Get("/api/v1/users/{id}", h.GetUser)

		// A3 — read-only group/role endpoints
		r.With(middleware.RequirePermission(middleware.PermReadGroups)).Get("/api/v1/groups", h.ListGroups)
		r.With(middleware.RequirePermission(middleware.PermReadGroups)).Get("/api/v1/roles", h.ListRealmRoles)

		r.With(middleware.RequirePermission(middleware.PermReadUsers)).Get("/api/v1/users/{id}/mfa-status", h.GetMFAStatus) // C2
		// B2: software inventory for a user's devices
		r.With(middleware.RequirePermission(middleware.PermReadCompliance)).Get("/api/v1/users/{id}/devices/software", h.GetDeviceSoftware)
		// B3: compliance posture
		r.With(middleware.RequirePermission(middleware.PermReadCompliance)).Get("/api/v1/users/{id}/devices/compliance", h.GetUserCompliance)
		r.With(middleware.RequirePermission(middleware.PermReadCompliance)).Get("/api/v1/compliance", h.GetOrgCompliance)
		// B2: list policies and teams
		r.With(middleware.RequirePermission(middleware.PermReadCompliance)).Get("/api/v1/policies", h.ListPolicies)
		r.With(middleware.RequirePermission(middleware.PermReadCompliance)).Get("/api/v1/teams", h.ListTeams)

		// Admin: drift / reconciliation report.
		r.With(middleware.RequirePermission(middleware.PermManageUsers)).Get("/api/v1/admin/drift", h.GetDrift)

		// C2: API token management (super-admin only).
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermManageAPITokens))
			r.Post("/api/v1/api-tokens", h.CreateAPIToken)
			r.Get("/api/v1/api-tokens", h.ListAPITokens)
			r.Delete("/api/v1/api-tokens/{id}", h.RevokeAPIToken)
		})

		// C3: Access review campaigns.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermReviewCampaigns))
			r.Get("/api/v1/campaigns", h.ListCampaigns)
			r.Get("/api/v1/campaigns/{id}/items", h.ListCampaignItems)
			r.Post("/api/v1/campaigns/{id}/items/{itemId}/decide", h.DecideCampaignItem)
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermManageCampaigns))
			r.Post("/api/v1/campaigns", h.CreateCampaign)
			r.Post("/api/v1/campaigns/{id}/complete", h.CompleteCampaign)
		})

		// C4: Self-service portal — available to all authenticated roles.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermSelfService))
			r.Get("/api/v1/portal/me/devices", h.PortalMyDevices)
			r.Get("/api/v1/portal/me/apps", h.PortalMyApps)
			r.Get("/api/v1/portal/me/compliance", h.PortalMyCompliance)
			r.Post("/api/v1/portal/access-requests", h.PortalRequestAccess)
		})
		// Admin approval queue for access requests.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermManageUsers))
			r.Get("/api/v1/portal/access-requests", h.AdminListAccessRequests)
			r.Patch("/api/v1/portal/access-requests/{id}", h.AdminDecideAccessRequest)
		})

		// D2: analytics time-series snapshots.
		r.With(middleware.RequirePermission(middleware.PermReadCompliance)).Get("/api/v1/analytics/snapshots", h.GetAnalyticsSnapshots)
	})
}
