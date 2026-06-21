package handlers

// A3 (FCEX2-7) — Per-app access policies.
//
// GET  /api/v1/apps/{appId}/policy — return the posture policy for an app.
// PUT  /api/v1/apps/{appId}/policy — create or replace the posture policy.
//
// The policy is stored in app_access_policies (Migration005). If no policy row
// exists for an app, GET returns a zero-value policy (all requirements false),
// meaning no posture gate is applied. PUT upserts the policy.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// AppAccessPolicy is the per-app posture requirement set.
type AppAccessPolicy struct {
	AppID                  string `json:"appId"`
	RequireEnrolled        bool   `json:"requireEnrolled"`
	RequireDiskEncrypted   bool   `json:"requireDiskEncrypted"`
	RequireNoCriticalVulns bool   `json:"requireNoCriticalVulns"`
	MaxOsAgeDays           *int   `json:"maxOsAgeDays,omitempty"`
	UpdatedAt              string `json:"updatedAt,omitempty"`
}

// GetAppAccessPolicy returns the posture policy for the given app.
// Returns the zero-value policy (no requirements) if none has been set.
func (h *Handler) GetAppAccessPolicy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		respondError(w, http.StatusBadRequest, "appId is required")
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	ctx := r.Context()

	var policy AppAccessPolicy
	policy.AppID = appID

	var updatedAt time.Time
	err := h.db.QueryRow(ctx,
		`SELECT require_enrolled, require_disk_encrypted, require_no_critical_vulns,
		        max_os_age_days, updated_at
		 FROM app_access_policies WHERE app_id = $1`,
		appID,
	).Scan(
		&policy.RequireEnrolled,
		&policy.RequireDiskEncrypted,
		&policy.RequireNoCriticalVulns,
		&policy.MaxOsAgeDays,
		&updatedAt,
	)
	if err != nil && err != pgx.ErrNoRows {
		h.logger.Error("failed to query app access policy", zap.String("app_id", appID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err == nil {
		policy.UpdatedAt = updatedAt.Format(time.RFC3339)
	}

	respondJSON(w, http.StatusOK, policy)
}

// UpsertAppAccessPolicyRequest is the JSON body for creating/updating an app policy.
type UpsertAppAccessPolicyRequest struct {
	RequireEnrolled        bool `json:"requireEnrolled"`
	RequireDiskEncrypted   bool `json:"requireDiskEncrypted"`
	RequireNoCriticalVulns bool `json:"requireNoCriticalVulns"`
	MaxOsAgeDays           *int `json:"maxOsAgeDays,omitempty"`
}

// UpsertAppAccessPolicy creates or replaces the posture policy for an app.
func (h *Handler) UpsertAppAccessPolicy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		respondError(w, http.StatusBadRequest, "appId is required")
		return
	}

	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	var req UpsertAppAccessPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()

	// Verify the app exists.
	var exists string
	err := h.db.QueryRow(ctx,
		`SELECT id FROM connected_apps WHERE id = $1`,
		appID,
	).Scan(&exists)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "app not found")
			return
		}
		h.logger.Error("failed to verify app existence", zap.String("app_id", appID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Upsert the policy.
	var updatedAt time.Time
	err = h.db.QueryRow(ctx,
		`INSERT INTO app_access_policies
		     (app_id, require_enrolled, require_disk_encrypted, require_no_critical_vulns, max_os_age_days, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (app_id) DO UPDATE SET
		     require_enrolled        = EXCLUDED.require_enrolled,
		     require_disk_encrypted  = EXCLUDED.require_disk_encrypted,
		     require_no_critical_vulns = EXCLUDED.require_no_critical_vulns,
		     max_os_age_days         = EXCLUDED.max_os_age_days,
		     updated_at              = NOW()
		 RETURNING updated_at`,
		appID, req.RequireEnrolled, req.RequireDiskEncrypted, req.RequireNoCriticalVulns, req.MaxOsAgeDays,
	).Scan(&updatedAt)
	if err != nil {
		h.logger.Error("failed to upsert app access policy", zap.String("app_id", appID), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Audit log.
	actorID := middleware.GetActorID(r.Context())
	if auditErr := h.writeAuditEntry(ctx, actorID, "app_policy_upsert", "app", appID, map[string]interface{}{
		"require_enrolled":          req.RequireEnrolled,
		"require_disk_encrypted":    req.RequireDiskEncrypted,
		"require_no_critical_vulns": req.RequireNoCriticalVulns,
	}); auditErr != nil {
		h.logger.Warn("failed to write audit log", zap.Error(auditErr))
	}

	policy := AppAccessPolicy{
		AppID:                  appID,
		RequireEnrolled:        req.RequireEnrolled,
		RequireDiskEncrypted:   req.RequireDiskEncrypted,
		RequireNoCriticalVulns: req.RequireNoCriticalVulns,
		MaxOsAgeDays:           req.MaxOsAgeDays,
		UpdatedAt:              updatedAt.Format(time.RFC3339),
	}
	respondJSON(w, http.StatusOK, policy)
}
