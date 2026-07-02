package handlers

import (
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// SetupRoutes registers all API routes on the provided chi router.
// authMW is applied to all /api/v1 routes except /health.
// A per-client rate limit protects the sensitive onboarding/offboarding
// endpoints from abuse.
//
// newLimiter constructs a rate limiter for a given (limit, window, name).
// name namespaces the limiter's counters (only meaningful for the
// Redis-backed implementation, which shares storage across limiter
// instances) and is otherwise unused. main.go supplies a factory that
// selects Redis or in-memory based on config (see middleware.NewLimiterFactory).
func SetupRoutes(r chi.Router, h *Handler, authMW func(http.Handler) http.Handler, newLimiter func(limit int, window time.Duration, name string) middleware.Limiter) {
	// Liveness/readiness probes (k8s-style) and the simple health endpoint.
	r.Get("/healthz", h.Healthz)
	r.Get("/readyz", h.Readyz)
	r.Get("/api/v1/health", h.Health)

	// The dependency health checks ping Keycloak/Fleet, so rate-limit them to
	// prevent unauthenticated amplification against those upstreams.
	healthLimiter := newLimiter(30, time.Minute, "health")
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
	deviceCookieLimiter := newLimiter(20, time.Minute, "device_cookie")
	r.Group(func(r chi.Router) {
		r.Use(deviceCookieLimiter.Middleware)
		r.Post("/api/v1/enrollment/device-identity", h.SetDeviceIdentityCookie)
	})

	// SCIM 2.0 discovery — unauthenticated per RFC 7644 §2.
	// Clients need these endpoints to discover capabilities before authenticating.
	r.Get("/scim/v2/ServiceProviderConfig", h.SCIMServiceProviderConfig)
	r.Get("/scim/v2/ResourceTypes", h.SCIMResourceTypes)
	r.Get("/scim/v2/Schemas", h.SCIMSchemas)

	// SCIM 2.0 provisioning — bearer-token authenticated, outside the user-JWT group.
	// SCIMBearerToken is injected by SetupSCIM (called from main after config load).
	// This is the LEGACY path: it authenticates on behalf of the Default
	// Organization for backward compatibility with existing Okta/Entra
	// integrations (see docs/adr/0005). Do not break it.
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

	// C4: org-scoped SCIM base path. Each org gets its own bearer token
	// (scim_bearer_tokens, Migration043) instead of sharing the legacy
	// SCIM_BEARER_TOKEN. {orgID} in the path must match the token's own org —
	// enforced by SCIMOrgBearerMiddleware, not by the handlers (which just
	// read the already-resolved OrgContext, same as every other org-scoped
	// route). Reuses the exact same handlers as the legacy path since they
	// are now org-context-aware.
	r.Route("/scim/v2/orgs/{orgID}", func(r chi.Router) {
		r.Use(h.SCIMOrgBearerMiddleware(h.db))
		r.Get("/Users", h.SCIMListUsers)
		r.Post("/Users", h.SCIMCreateUser)
		r.Get("/Users/{id}", h.SCIMGetUser)
		r.Patch("/Users/{id}", h.SCIMPatchUser)
		r.Delete("/Users/{id}", h.SCIMDeleteUser)
		r.Get("/Groups", h.SCIMListGroups)
		r.Post("/Groups", h.SCIMCreateGroup)
		r.Get("/Groups/{id}", h.SCIMGetGroup)
		r.Patch("/Groups/{id}", h.SCIMPatchGroup)
		r.Delete("/Groups/{id}", h.SCIMDeleteGroup)
	})

	// A1: access evaluation — bearer-token authenticated, outside the user-JWT group.
	// Called by the Keycloak authenticator SPI (or any service) to gate SSO on posture.
	r.Group(func(r chi.Router) {
		r.Use(h.accessEvalBearerMW)
		r.Post("/api/v1/access/evaluate", h.EvaluateAccess)
	})

	// C3: self-service password reset — public, no JWT. Rate-limited to
	// prevent email flooding. Returns a fixed message to avoid user enumeration.
	forgotLimiter := newLimiter(10, time.Minute, "forgot_password")
	r.Group(func(r chi.Router) {
		r.Use(forgotLimiter.Middleware)
		r.Post("/api/v1/auth/forgot-password", h.ForgotPassword)
	})

	// B1 — First-run setup. Unauthenticated but fail-closed once provisioned.
	// Rate-limited to prevent brute-force attempts on the setup window.
	setupLimiter := newLimiter(10, time.Minute, "setup")
	r.Group(func(r chi.Router) {
		r.Use(setupLimiter.Middleware)
		r.Get("/api/v1/setup/status", h.SetupStatus)
		r.Post("/api/v1/setup", h.Setup)
	})

	// Rate limiter for mutating endpoints: 20 requests / minute / client.
	mutateLimiter := newLimiter(20, time.Minute, "mutate")

	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.ActorIDMiddleware)
		// C1: resolve the active organization for every authenticated request.
		// Fails closed (403) when no org context can be resolved — see
		// middleware.OrgContextMiddleware for the resolution order.
		r.Use(middleware.OrgContextMiddleware(h.db))

		// C2: org + membership management (system-admin creates orgs; org-admin
		// manages their own org's members).
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermManageOrgs))
			r.Post("/api/v1/orgs", h.CreateOrg)
			r.Get("/api/v1/orgs", h.ListOrgs)
		})
		r.With(middleware.RequirePermission(middleware.PermSelfService)).Get("/api/v1/me", h.Me)
		r.Group(func(r chi.Router) {
			// Org-scoped: system-admin (any org) or org-admin (their own org,
			// enforced inside the handlers against the resolved OrgContext).
			r.Use(middleware.RequireOrgAdminOrSystemAdmin)
			r.Get("/api/v1/orgs/{orgId}/members", h.ListOrgMembers)
			r.Post("/api/v1/orgs/{orgId}/members", h.AddOrgMember)
			r.Delete("/api/v1/orgs/{orgId}/members/{userId}", h.RemoveOrgMember)
		})

		// Sensitive write endpoints get the stricter rate limit.
		r.Group(func(r chi.Router) {
			r.Use(mutateLimiter.Middleware)
			r.With(middleware.RequirePermission(middleware.PermOnboardOffboard)).Post("/api/v1/onboard", h.Onboard)
			r.With(middleware.RequirePermission(middleware.PermOnboardOffboard)).Post("/api/v1/onboard/bulk", h.BulkOnboard) // C1
			r.With(middleware.RequirePermission(middleware.PermOnboardOffboard)).Post("/api/v1/offboard/{userId}", h.Offboard)
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Post("/api/v1/apps/create", h.CreateApp)
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Post("/api/v1/apps/{appId}/assign", h.AssignApp)
			// B4: App Catalog — create from template
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Post("/api/v1/apps/templates/{templateId}/create", h.CreateAppFromTemplate)

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
			// E1: expanded MDM command set
			r.With(middleware.RequirePermission(middleware.PermManageDevices)).Post("/api/v1/devices/{id}/restart", h.RemoteRestart)
			r.With(middleware.RequirePermission(middleware.PermManageDevices)).Post("/api/v1/devices/{id}/lock-message", h.RemoteLockWithMessage)
			r.With(middleware.RequirePermission(middleware.PermManageDevices)).Post("/api/v1/devices/{id}/clear-passcode", h.RemoteClearPasscode)

			// A3: per-app access policy write
			r.With(middleware.RequirePermission(middleware.PermManagePolicies)).Put("/api/v1/apps/{appId}/policy", h.UpsertAppAccessPolicy)
			// D2: per-app policy preview (dry-run eval)
			r.With(middleware.RequirePermission(middleware.PermManagePolicies)).Post("/api/v1/apps/{appId}/policy/preview", h.PreviewAppPolicy)

			// B2: Fleet team management (team-scoped MDM policies)
			r.With(middleware.RequirePermission(middleware.PermManagePolicies)).Post("/api/v1/teams", h.CreateTeam)
			r.With(middleware.RequirePermission(middleware.PermManagePolicies)).Post("/api/v1/teams/{id}/policies", h.AssignTeamPolicy)
			r.With(middleware.RequirePermission(middleware.PermManageDevices)).Post("/api/v1/teams/{id}/hosts", h.MoveHostToTeam)

			// A4: Outbound provisioning config + resync — write endpoints.
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Put("/api/v1/apps/{appId}/provisioning", h.UpsertProvisioningConfig)
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Post("/api/v1/apps/{appId}/provisioning/resync/{userId}", h.ResyncUser)
			// E1: Provisioning dry-run preview
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Post("/api/v1/apps/{appId}/provisioning/dry-run", h.DryRunProvisioning)
			// E2: Reconcile all provisioning records
			r.With(middleware.RequirePermission(middleware.PermManageApps)).Post("/api/v1/apps/{appId}/provisioning/reconcile-all", h.ReconcileAllHandler)
			// E3: Review schedule writes
			r.With(middleware.RequirePermission(middleware.PermManageCampaigns)).Post("/api/v1/review-schedules", h.CreateReviewSchedule)
			r.With(middleware.RequirePermission(middleware.PermManageCampaigns)).Patch("/api/v1/review-schedules/{id}", h.UpdateReviewSchedule)
			r.With(middleware.RequirePermission(middleware.PermManageCampaigns)).Delete("/api/v1/review-schedules/{id}", h.DeleteReviewSchedule)
			// C1: LDAP/AD federation sources — write endpoints.
			r.With(middleware.RequirePermission(middleware.PermManageUsers)).Post("/api/v1/federation/sources", h.CreateFederationSource)
			r.With(middleware.RequirePermission(middleware.PermManageUsers)).Patch("/api/v1/federation/sources/{id}", h.UpdateFederationSource)
			r.With(middleware.RequirePermission(middleware.PermManageUsers)).Delete("/api/v1/federation/sources/{id}", h.DeleteFederationSource)
			r.With(middleware.RequirePermission(middleware.PermManageUsers)).Post("/api/v1/federation/sources/{id}/test", h.TestFederationConnection)
			r.With(middleware.RequirePermission(middleware.PermManageUsers)).Post("/api/v1/federation/sources/{id}/sync", h.TriggerFederationSync)
		})

		r.With(middleware.RequirePermission(middleware.PermSelfService)).Post("/api/v1/auth/device-check", h.DeviceCheck)
		r.With(middleware.RequirePermission(middleware.PermReadApps)).Get("/api/v1/apps", h.ListApps)
		// C2: IdP-initiated SSO URL for a SAML app
		r.With(middleware.RequirePermission(middleware.PermReadApps)).Get("/api/v1/apps/{appId}/saml/idp-url", h.GetSAMLIdPInitiatedURL)
		// C3: SAML IdP metadata XML download
		r.With(middleware.RequirePermission(middleware.PermReadApps)).Get("/api/v1/apps/{appId}/saml/metadata", h.GetSAMLMetadata)
		// B4: App Catalog — list templates
		r.With(middleware.RequirePermission(middleware.PermReadApps)).Get("/api/v1/apps/templates", h.ListAppTemplates)
		// A3: per-app access policy read
		r.With(middleware.RequirePermission(middleware.PermReadApps)).Get("/api/v1/apps/{appId}/policy", h.GetAppAccessPolicy)
		// A4: Outbound provisioning config + state — read endpoints.
		r.With(middleware.RequirePermission(middleware.PermManageApps)).Get("/api/v1/apps/{appId}/provisioning", h.GetProvisioningConfig)
		r.With(middleware.RequirePermission(middleware.PermManageApps)).Get("/api/v1/apps/{appId}/provisioning/state", h.ListProvisioningState)
		r.With(middleware.RequirePermission(middleware.PermReadAuditLogs)).Get("/api/v1/audit-logs", h.ListAuditLogs)
		r.With(middleware.RequirePermission(middleware.PermExportAuditLogs)).Get("/api/v1/audit-logs/export", h.ExportAuditLogs)    // C4
		r.With(middleware.RequirePermission(middleware.PermReadAuditLogs)).Get("/api/v1/audit-logs/verify", h.VerifyAuditChain)     // C1
		r.With(middleware.RequirePermission(middleware.PermReadAuditLogs)).Get("/api/v1/audit-logs/integrity", h.GetAuditIntegrity) // B3
		r.With(middleware.RequirePermission(middleware.PermExportAuditLogs)).Get("/api/v1/reports", h.DownloadReport)               // B2
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
		// E2: device command history
		r.With(middleware.RequirePermission(middleware.PermReadCompliance)).Get("/api/v1/devices/{id}/commands", h.GetDeviceCommandHistory)
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

		// E3: Review schedules read + campaign export
		r.With(middleware.RequirePermission(middleware.PermReviewCampaigns)).Get("/api/v1/review-schedules", h.ListReviewSchedules)
		r.With(middleware.RequirePermission(middleware.PermReviewCampaigns)).Get("/api/v1/campaigns/{id}/export", h.ExportCampaign)

		// C4: Self-service portal — available to all authenticated roles.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermSelfService))
			r.Get("/api/v1/portal/me/devices", h.PortalMyDevices)
			r.Get("/api/v1/portal/me/apps", h.PortalMyApps)
			r.Get("/api/v1/portal/me/compliance", h.PortalMyCompliance)
			r.Post("/api/v1/portal/access-requests", h.PortalRequestAccess)

			// B1: MFA self-service enrollment (scoped to calling user only).
			r.Get("/api/v1/portal/me/mfa/factors", h.PortalMyMFAFactors)
			r.Post("/api/v1/portal/me/mfa/totp/enroll", h.PortalEnrollTOTP)
			r.Post("/api/v1/portal/me/mfa/webauthn/enroll", h.PortalEnrollWebAuthn)
			r.Delete("/api/v1/portal/me/mfa/factors/{credId}", h.PortalRemoveMFAFactor)
			r.Get("/api/v1/portal/me/recovery-codes", h.PortalRecoveryCodesStatus)
			r.Post("/api/v1/portal/me/recovery-codes", h.PortalGenerateRecoveryCodes)
		})
		// Admin approval queue for access requests.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermManageUsers))
			r.Get("/api/v1/portal/access-requests", h.AdminListAccessRequests)
			r.Patch("/api/v1/portal/access-requests/{id}", h.AdminDecideAccessRequest)
		})

		// D2: analytics time-series snapshots.
		r.With(middleware.RequirePermission(middleware.PermReadCompliance)).Get("/api/v1/analytics/snapshots", h.GetAnalyticsSnapshots)

		// D1: password & account policy (super-admin only).
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Get("/api/v1/account-policy", h.GetAccountPolicy)
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Put("/api/v1/account-policy", h.UpdateAccountPolicy)

		// D1: Fleet server configuration (super-admin only).
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Get("/api/v1/settings/fleet", h.GetFleetConfig)
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Put("/api/v1/settings/fleet", h.UpsertFleetConfig)
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Post("/api/v1/settings/fleet/test", h.TestFleetConfig)

		// D2: SMTP configuration (super-admin only).
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Get("/api/v1/settings/smtp", h.GetSMTPConfig)
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Put("/api/v1/settings/smtp", h.UpsertSMTPConfig)
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Post("/api/v1/settings/smtp/test", h.TestSMTPEmail)

		// D3: Identity provider management (super-admin only).
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Get("/api/v1/settings/identity-providers", h.ListIdentityProviders)
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Post("/api/v1/settings/identity-providers", h.CreateIdentityProvider)
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Put("/api/v1/settings/identity-providers/{alias}", h.UpdateIdentityProvider)
		r.With(middleware.RequirePermission(middleware.PermManageAccountPolicy)).Delete("/api/v1/settings/identity-providers/{alias}", h.DeleteIdentityProvider)

		// C4 (FCEX3-16) — Approval workflow.
		// Helpdesk submits a request; super-admin approves/rejects via PermApproveRequests.
		r.With(middleware.RequirePermission(middleware.PermSubmitApprovals)).Post("/api/v1/approval-requests", h.SubmitApproval)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(middleware.PermApproveRequests))
			r.Get("/api/v1/approval-requests", h.ListApprovalRequests)
			r.Patch("/api/v1/approval-requests/{id}", h.DecideApproval)
		})

		// C1: LDAP/AD federation sources — read endpoints.
		r.With(middleware.RequirePermission(middleware.PermManageUsers)).Get("/api/v1/federation/sources", h.ListFederationSources)
		r.With(middleware.RequirePermission(middleware.PermManageUsers)).Get("/api/v1/federation/sources/{id}", h.GetFederationSource)
	})

	// Test-only enrollment-token helper — ONLY registered when APP_ENV=test.
	// Gated by the SCIM bearer token; never reaches production because the
	// APP_ENV guard prevents registration outside of test stacks.
	if os.Getenv("APP_ENV") == "test" {
		r.Group(func(r chi.Router) {
			r.Use(h.scimBearerMW)
			r.Post("/api/v1/e2e/enrollment-token", h.E2ECreateEnrollmentToken)
			// C5: org+admin-token seeding for the cross-org isolation e2e suite.
			r.Post("/api/v1/e2e/seed-org", h.E2ESeedOrgWithAdminToken)
		})
	}
}
