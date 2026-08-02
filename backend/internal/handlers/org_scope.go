package handlers

// Org-scoping gate wrappers. The core implementation lives in
// internal/orgscope (zero domain deps); these thin methods keep the
// handler call sites readable. See docs/HANDLERS_OWNERSHIP.md step 2.

import (
	"context"
	"net/http"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
	"github.com/FisiFla/freecloud/backend/internal/orgscope"
)

// resourceInOrg reports whether the resource belongs to orgID (see
// orgscope.CheckResourceInOrg).
func (h *Handler) resourceInOrg(ctx context.Context, table, idColumn, id, orgID string) (bool, error) {
	return orgscope.CheckResourceInOrg(ctx, h.db, table, idColumn, id, orgID)
}

// requireResourceInCallerOrg is the shared "verify then 404" gate (see
// orgscope.RequireResourceInCallerOrg).
func (h *Handler) requireResourceInCallerOrg(w http.ResponseWriter, r *http.Request, table, idColumn, id, notFoundMsg string) bool {
	return orgscope.RequireResourceInCallerOrg(w, r, h.db, h.logger, table, idColumn, id, notFoundMsg)
}

// requireCampaignInCallerOrg verifies a review_campaigns id belongs to the
// caller's active org.
func (h *Handler) requireCampaignInCallerOrg(w http.ResponseWriter, r *http.Request, campaignID string) bool {
	return h.requireResourceInCallerOrg(w, r, "review_campaigns", "id", campaignID, "campaign not found")
}

// requireFederationSourceInCallerOrg verifies a federation_sources id belongs
// to the caller's active org.
func (h *Handler) requireFederationSourceInCallerOrg(w http.ResponseWriter, r *http.Request, sourceID string) bool {
	return h.requireResourceInCallerOrg(w, r, "federation_sources", "id", sourceID, "federation source not found")
}

// requireReviewScheduleInCallerOrg verifies a review_schedules id belongs to
// the caller's active org.
func (h *Handler) requireReviewScheduleInCallerOrg(w http.ResponseWriter, r *http.Request, scheduleID string) bool {
	return h.requireResourceInCallerOrg(w, r, "review_schedules", "id", scheduleID, "schedule not found")
}

// requireAppInCallerOrg verifies a connected_apps id belongs to the caller's
// active org. Used by every app-scoped sub-resource handler (provisioning
// config/state, access policy, SAML metadata, ...) that takes {appId} from
// the path — those sub-resources have no org_id of their own, so ownership
// is always proven through the parent app.
func (h *Handler) requireAppInCallerOrg(w http.ResponseWriter, r *http.Request, appID string) bool {
	return h.requireResourceInCallerOrg(w, r, "connected_apps", "id", appID, "app not found")
}

// isSystemAdminCaller reports whether the authenticated caller holds the
// global system-admin role (RoleSuperAdmin), as opposed to an org-scoped
// admin/member. Used by read endpoints that otherwise expose realm/fleet-
// wide data across every tenant (M1): only a system admin sees the
// unfiltered view; every other caller is restricted to their own org (or
// denied entirely where there is no per-org scoping to fall back to).
func isSystemAdminCaller(ctx context.Context) bool {
	claims := middleware.GetClaims(ctx)
	return claims != nil && claims.Role == middleware.RoleSuperAdmin
}
