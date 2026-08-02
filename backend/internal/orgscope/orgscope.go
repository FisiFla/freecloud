// Package orgscope provides shared multi-tenant org-scoping helpers (Epic C /
// v1.7 multi-tenant). See docs/HANDLERS_OWNERSHIP.md step 2.
//
// Every tenant-scoped resource lookup by a bare ID (device host ID, user ID,
// campaign ID, ...) MUST verify the resource belongs to the caller's active
// org before acting on it — otherwise an org-B admin who merely knows or
// guesses an org-A resource's ID can read or mutate it. These helpers
// centralize that check so every handler applies it the same way: fail
// closed (404, indistinguishable from "doesn't exist") on a foreign-org or
// missing resource.
package orgscope

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/httputil"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// RowQueryer is the subset of DBPool handlers need for org-scope checks.
type RowQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// allowedOrgTables is a whitelist of table/column pairs CheckResourceInOrg may
// query. SQL identifiers cannot be parameterized, so concatenation is only
// safe when both table and idColumn come from this map — never request input.
var allowedOrgTables = map[string]string{
	"review_campaigns":   "id",
	"federation_sources": "id",
	"review_schedules":   "id",
	"connected_apps":     "id",
	"users":              "keycloak_user_id",
	"devices":            "fleet_host_id",
}

// CheckResourceInOrg runs a `SELECT 1 FROM <table> WHERE <idColumn> = $1 AND
// org_id = $2` existence check and reports whether the resource belongs to
// orgID. A non-existent resource and a foreign-org resource are
// indistinguishable here (both return false, nil) — this is intentional:
// the caller should respond 404 either way, never leaking whether an ID
// exists in some OTHER org.
func CheckResourceInOrg(ctx context.Context, q RowQueryer, table, idColumn, id, orgID string) (bool, error) {
	if q == nil {
		return false, errors.New("database not available")
	}
	wantCol, ok := allowedOrgTables[table]
	if !ok || wantCol != idColumn {
		return false, errors.New("orgscope: table/column not in allowlist")
	}
	var found int
	err := q.QueryRow(ctx,
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

// RequireResourceInCallerOrg is the shared "verify then 404" gate: resolve the
// caller's org context (403 if missing), then confirm the resource exists AND
// belongs to that org (404 otherwise — never distinguishing "doesn't exist"
// from "belongs to a different org"). Returns false (and has already written
// the response) when the caller should stop; true means it's safe to proceed.
func RequireResourceInCallerOrg(
	w http.ResponseWriter, r *http.Request, q RowQueryer, logger *zap.Logger,
	table, idColumn, id, notFoundMsg string,
) bool {
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		httputil.RespondError(w, http.StatusForbidden, "forbidden: no organization context")
		return false
	}
	ok, err := CheckResourceInOrg(r.Context(), q, table, idColumn, id, oc.OrgID)
	if err != nil {
		logger.Error("failed to verify resource org ownership", zap.String("table", table), zap.Error(err))
		httputil.RespondError(w, http.StatusInternalServerError, "internal error")
		return false
	}
	if !ok {
		httputil.RespondError(w, http.StatusNotFound, notFoundMsg)
		return false
	}
	return true
}
