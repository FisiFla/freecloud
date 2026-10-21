package handlers

import (
	"net/http"

	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/audit"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// VerifyAuditChain walks all audit_logs rows in seq order and reports whether
// the hash chain is intact. Returns the first broken link if found.
//
// Route: GET /api/v1/audit-logs/verify
//
// The chain is GLOBAL: it spans every tenant's rows (ADR 0005 keeps one
// chain, org_id is a filter column only). Walking it therefore exposes
// cross-tenant metadata — the first break's sequence number and the error
// string with the expected and actual hashes — to every caller.
//
// The route's permission gate (PermReadAuditLogs) is held by the auditor
// role as well as the super-admin, which is too broad for a global-chain
// view, so the handler enforces the stricter SYSTEM-ADMIN requirement
// itself: only a system administrator may inspect the global chain
// (issue #133).
func (h *Handler) VerifyAuditChain(w http.ResponseWriter, r *http.Request) {
	// Fail closed: no identity, or an identity that is not a system
	// administrator, gets no information about the global chain.
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.Role != middleware.RoleSuperAdmin {
		respondError(w, http.StatusForbidden, "forbidden: audit chain verification requires system-admin")
		return
	}

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
