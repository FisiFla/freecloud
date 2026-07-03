package handlers

// Shared org-scoping helpers (Epic C / v1.7 multi-tenant).
//
// Every tenant-scoped resource lookup by a bare ID (device host ID, user ID,
// campaign ID, ...) MUST verify the resource belongs to the caller's active
// org before acting on it — otherwise an org-B admin who merely knows or
// guesses an org-A resource's ID can read or mutate it. These helpers
// centralize that check so every handler applies it the same way: fail
// closed (404, indistinguishable from "doesn't exist") on a foreign-org or
// missing resource.

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// resourceInOrg runs a `SELECT 1 FROM <table> WHERE <idColumn> = $1 AND
// org_id = $2` existence check and reports whether the resource belongs to
// orgID. A non-existent resource and a foreign-org resource are
// indistinguishable here (both return false, nil) — this is intentional:
// the caller should respond 404 either way, never leaking whether an ID
// exists in some OTHER org.
func (h *Handler) resourceInOrg(ctx context.Context, table, idColumn, id, orgID string) (bool, error) {
	if h.db == nil {
		return false, errors.New("database not available")
	}
	var found int
	err := h.db.QueryRow(ctx,
		`SELECT 1 FROM `+table+` WHERE `+idColumn+` = $1 AND org_id = $2`,
		id, orgID,
	).Scan(&found)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// requireResourceInCallerOrg is the shared "verify then 404" gate used by
// every single-resource handler in this sweep: resolve the caller's org
// context (403 if missing), then confirm the resource exists AND belongs to
// that org (404 otherwise — never distinguishing "doesn't exist" from
// "belongs to a different org"). Returns false (and has already written the
// response) when the caller should stop; true means it's safe to proceed.
func (h *Handler) requireResourceInCallerOrg(w http.ResponseWriter, r *http.Request, table, idColumn, id, notFoundMsg string) bool {
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return false
	}
	ok, err := h.resourceInOrg(r.Context(), table, idColumn, id, oc.OrgID)
	if err != nil {
		h.logger.Error("failed to verify resource org ownership", zap.String("table", table), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return false
	}
	if !ok {
		respondError(w, http.StatusNotFound, notFoundMsg)
		return false
	}
	return true
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

// requireAppInCallerOrg verifies a connected_apps id belongs to the caller's
// active org. Used by every app-scoped sub-resource handler (provisioning
// config/state, access policy, SAML metadata, ...) that takes {appId} from
// the path — those sub-resources have no org_id of their own, so ownership
// is always proven through the parent app.
func (h *Handler) requireAppInCallerOrg(w http.ResponseWriter, r *http.Request, appID string) bool {
	return h.requireResourceInCallerOrg(w, r, "connected_apps", "id", appID, "app not found")
}
