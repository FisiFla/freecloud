package handlers

import (
	"net/http"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/reconcile"
)

// GetDrift runs an on-demand reconciliation pass and returns the drift report.
// Admin-only: protected by authMW inside the admin route group in routes.go.
// Report-only — never mutates Keycloak or the DB.
func (h *Handler) GetDrift(w http.ResponseWriter, r *http.Request) {
	if h.reconciler == nil {
		respondError(w, http.StatusServiceUnavailable, "reconciliation not configured")
		return
	}
	result, err := h.reconciler.Run(r.Context())
	if err != nil {
		h.logger.Error("drift check failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "drift check failed")
		return
	}
	type driftResponse struct {
		OrphansInKeycloak []string `json:"orphans_in_keycloak"`
		OrphansInDB       []string `json:"orphans_in_db"`
	}
	resp := driftResponse{
		OrphansInKeycloak: result.OrphansInKeycloak,
		OrphansInDB:       result.OrphansInDB,
	}
	if resp.OrphansInKeycloak == nil {
		resp.OrphansInKeycloak = []string{}
	}
	if resp.OrphansInDB == nil {
		resp.OrphansInDB = []string{}
	}
	respondJSON(w, http.StatusOK, resp)
}

// SetReconciler attaches a reconciler to the handler so GetDrift can use it.
// Called once at startup from main.
func (h *Handler) SetReconciler(rec *reconcile.Reconciler) {
	h.reconciler = rec
}
