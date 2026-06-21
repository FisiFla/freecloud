package handlers

import (
	"net/http"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/audit"
)

// VerifyAuditChain walks all audit_logs rows in seq order and reports whether
// the hash chain is intact. Returns the first broken link if found.
//
// Route: GET /api/v1/audit-logs/verify
// Permission-gated via PermReadAuditLogs in routes.go.
func (h *Handler) VerifyAuditChain(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respondError(w, http.StatusInternalServerError, "database not available")
		return
	}

	result, err := audit.VerifyChain(r.Context(), h.db)
	if err != nil {
		h.logger.Error("audit chain verification failed", zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	status := http.StatusOK
	if !result.OK {
		status = http.StatusUnprocessableEntity
	}
	respondJSON(w, status, result)
}
