package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/FisiFla/freecloud/backend/internal/audit"
	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

const auditWriteTimeout = 5 * time.Second

// auditOrgIDFromContext returns the org the current request acts on, for audit
// attribution.
//
// The org is resolved from the request context because OrgContextMiddleware
// has already validated it: the caller is a member of that org (or a system
// admin acting on it). When no org context is present — a system-initiated
// write such as a Fleet enrollment callback — the entry is attributed to the
// Default Organization, which is the only org such a write can be proven to
// belong to.
func auditOrgIDFromContext(ctx context.Context) string {
	if ctx != nil {
		if oc := middleware.GetOrgContext(ctx); oc != nil && oc.OrgID != "" {
			return oc.OrgID
		}
	}
	return middleware.DefaultOrgID
}

func writeAuditEntry(ctx context.Context, db audit.DBPool, actorID, action, targetType, targetID string, details any) error {
	if db == nil {
		return errors.New("audit: database not configured")
	}
	return audit.WriteEntry(ctx, db, auditOrgIDFromContext(ctx), actorID, action, targetType, targetID, details)
}

// writeAuditEntryWithOrg writes an audit entry attributed to an explicit org.
// Used where the org was captured before the context was detached.
func writeAuditEntryWithOrg(ctx context.Context, db audit.DBPool, orgID, actorID, action, targetType, targetID string, details any) error {
	if db == nil {
		return errors.New("audit: database not configured")
	}
	if orgID == "" {
		orgID = middleware.DefaultOrgID
	}
	return audit.WriteEntry(ctx, db, orgID, actorID, action, targetType, targetID, details)
}

func (h *Handler) writeAuditEntry(ctx context.Context, actorID, action, targetType, targetID string, details any) error {
	if h.db == nil {
		return errors.New("audit: database not configured")
	}
	return writeAuditEntry(ctx, h.db, actorID, action, targetType, targetID, details)
}

// writeAuditEntryBestEffort writes an audit entry using a detached background
// context with a 5-second timeout. It is best-effort: on failure it returns the
// error (callers typically log it) but never blocks the core action's success.
//
// The org is captured from the request context before the context is detached,
// because a detached context no longer carries the caller's org.
func (h *Handler) writeAuditEntryBestEffort(ctx context.Context, actorID, action, targetType, targetID string, details any) error {
	if h.db == nil {
		return errors.New("audit: database not configured")
	}
	orgID := auditOrgIDFromContext(ctx)
	detached, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
	defer cancel()
	return writeAuditEntryWithOrg(detached, h.db, orgID, actorID, action, targetType, targetID, details)
}
