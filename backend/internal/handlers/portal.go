package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// portalUserID extracts the caller's Keycloak sub from context.
// Fail-closed: returns empty string when no claims are present.
func portalUserID(r *http.Request) string {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.Sub == "" {
		return ""
	}
	return claims.Sub
}

// PortalMyDevices returns devices assigned to the calling user.
// Route: GET /api/v1/portal/me/devices
func (h *Handler) PortalMyDevices(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT d.fleet_host_id, COALESCE(d.hostname, ''), COALESCE(d.os_version, ''),
		        d.last_seen_at, d.created_at
		 FROM devices d
		 JOIN users_devices_mapping m ON m.device_id = d.fleet_host_id
		 WHERE m.user_id = $1`,
		uid,
	)
	if err != nil {
		h.logger.Error("portal: failed to list devices", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type portalDevice struct {
		FleetHostID string  `json:"fleetHostId"`
		Hostname    string  `json:"hostname"`
		OsVersion   string  `json:"osVersion"`
		LastSeenAt  *string `json:"lastSeenAt,omitempty"`
		CreatedAt   string  `json:"createdAt"`
	}
	devices := []portalDevice{}
	for rows.Next() {
		var d portalDevice
		var lastSeenAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&d.FleetHostID, &d.Hostname, &d.OsVersion, &lastSeenAt, &createdAt); err != nil {
			h.logger.Error("portal: failed to scan device row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		d.CreatedAt = createdAt.Format(time.RFC3339)
		if lastSeenAt != nil {
			s := lastSeenAt.Format(time.RFC3339)
			d.LastSeenAt = &s
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("portal: failed to iterate device rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, devices)
}

// PortalMyApps returns apps assigned to the calling user.
// Route: GET /api/v1/portal/me/apps
func (h *Handler) PortalMyApps(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT ca.id, ca.name, COALESCE(ca.base_url, ''), ca.protocol, ca.enabled
		 FROM connected_apps ca
		 JOIN app_assignments aa ON aa.app_id = ca.id
		 WHERE aa.user_id = $1 AND ca.enabled = true`,
		uid,
	)
	if err != nil {
		h.logger.Error("portal: failed to list apps", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type portalApp struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		BaseURL  string `json:"baseUrl"`
		Protocol string `json:"protocol"`
		Enabled  bool   `json:"enabled"`
	}
	apps := []portalApp{}
	for rows.Next() {
		var a portalApp
		if err := rows.Scan(&a.ID, &a.Name, &a.BaseURL, &a.Protocol, &a.Enabled); err != nil {
			h.logger.Error("portal: failed to scan app row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		apps = append(apps, a)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("portal: failed to iterate app rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, apps)
}

// PortalMyCompliance returns the calling user's device compliance posture.
// Delegates to GetUserCompliance, scoped to the caller's sub.
// Route: GET /api/v1/portal/me/compliance
func (h *Handler) PortalMyCompliance(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}
	// Inject caller's ID as the chi URL param, then delegate.
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", uid)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chiCtx))
	h.GetUserCompliance(w, r)
}

// AccessRequestPayload is the body for POST /api/v1/portal/access-requests.
type AccessRequestPayload struct {
	AppID  string `json:"appId"`
	Reason string `json:"reason"`
}

// PortalRequestAccess submits an access request for an app.
// Route: POST /api/v1/portal/access-requests
func (h *Handler) PortalRequestAccess(w http.ResponseWriter, r *http.Request) {
	uid := portalUserID(r)
	if uid == "" {
		respondError(w, http.StatusUnauthorized, "unauthorized: valid JWT required")
		return
	}
	var req AccessRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !isValidUUID(req.AppID) {
		respondError(w, http.StatusBadRequest, "appId must be a valid UUID")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}

	// Tenant isolation: app_id must belong to the caller's org. Without this,
	// a user who learns another org's app UUID can open a pending request that
	// an approver then grants as a cross-org assignment.
	if !h.requireAppInCallerOrg(w, r, req.AppID) {
		return
	}

	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO access_requests (requester_id, app_id, reason, org_id)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (requester_id, app_id, status) DO NOTHING
		 RETURNING id`,
		uid, req.AppID, req.Reason, oc.OrgID,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING returns no row — treat as conflict.
			respondError(w, http.StatusConflict, "a pending request for this app already exists")
			return
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			respondError(w, http.StatusNotFound, "app or requester not found")
			return
		}
		h.logger.Error("portal: failed to create access request", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// AdminListAccessRequests lists all pending access requests.
// Route: GET /api/v1/portal/access-requests (requires PermManageUsers via middleware)
func (h *Handler) AdminListAccessRequests(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT ar.id, ar.requester_id, ar.app_id::text, ar.status,
		        COALESCE(ar.reason, ''), COALESCE(ar.decided_by, ''), ar.created_at
		 FROM access_requests ar
		 WHERE ar.status = 'pending' AND ar.org_id = $1
		 ORDER BY ar.created_at`,
		oc.OrgID,
	)
	if err != nil {
		h.logger.Error("admin: failed to list access requests", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type accessReq struct {
		ID          string `json:"id"`
		RequesterID string `json:"requesterId"`
		AppID       string `json:"appId"`
		Status      string `json:"status"`
		Reason      string `json:"reason"`
		DecidedBy   string `json:"decidedBy"`
		CreatedAt   string `json:"createdAt"`
	}
	reqs := []accessReq{}
	for rows.Next() {
		var req accessReq
		var createdAt time.Time
		if err := rows.Scan(&req.ID, &req.RequesterID, &req.AppID, &req.Status,
			&req.Reason, &req.DecidedBy, &createdAt); err != nil {
			h.logger.Error("admin: failed to scan access request row", zap.Error(err))
			respondError(w, http.StatusInternalServerError, "internal error")
			return
		}
		req.CreatedAt = createdAt.Format(time.RFC3339)
		reqs = append(reqs, req)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("admin: failed to iterate access request rows", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondJSON(w, http.StatusOK, reqs)
}

// AdminDecideAccessRequest approves or rejects an access request.
// On approval, creates the app_assignments row.
// Route: PATCH /api/v1/portal/access-requests/{id} (requires PermManageUsers via middleware)
func (h *Handler) AdminDecideAccessRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Decision string `json:"decision"` // "approved" or "rejected"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Decision != "approved" && req.Decision != "rejected" {
		respondError(w, http.StatusBadRequest, "decision must be 'approved' or 'rejected'")
		return
	}
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}
	oc := middleware.GetOrgContext(r.Context())
	if oc == nil {
		respondError(w, http.StatusForbidden, "forbidden: no organization context")
		return
	}
	actorID := middleware.GetActorID(r.Context())
	ctx := r.Context()

	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.logger.Error("failed to begin access request decision transaction", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var requesterID, appID string
	err = tx.QueryRow(ctx,
		`UPDATE access_requests
		 SET status = $1, decided_by = $2, decided_at = NOW()
		 WHERE id = $3 AND status = 'pending' AND org_id = $4
		 RETURNING requester_id, app_id::text`,
		req.Decision, actorID, id, oc.OrgID,
	).Scan(&requesterID, &appID)
	if err != nil {
		h.logger.Error("failed to decide access request", zap.Error(err))
		respondError(w, http.StatusNotFound, "request not found or already decided")
		return
	}

	// If approved, create the app assignment (idempotent). Re-check the app
	// still belongs to this org so a stale/cross-org app_id can never grant
	// access even if it somehow landed in access_requests.
	if req.Decision == "approved" {
		var appOrg string
		if err := tx.QueryRow(ctx,
			`SELECT org_id::text FROM connected_apps WHERE id = $1::uuid`, appID,
		).Scan(&appOrg); err != nil || appOrg != oc.OrgID {
			respondError(w, http.StatusNotFound, "app not found")
			return
		}
		_, assignErr := tx.Exec(ctx,
			`INSERT INTO app_assignments (app_id, user_id, assigned_by)
			 VALUES ($1::uuid, $2, $3)
			 ON CONFLICT DO NOTHING`,
			appID, requesterID, actorID,
		)
		if assignErr != nil {
			h.logger.Error("access request approval assignment insert failed", zap.Error(assignErr))
			respondError(w, http.StatusInternalServerError, "failed to grant requested access")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("failed to commit access request decision", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": req.Decision})
}
