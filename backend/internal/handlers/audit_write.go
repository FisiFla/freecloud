package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/FisiFla/freecloud/backend/internal/audit"
)

const auditWriteTimeout = 5 * time.Second

func writeAuditEntry(ctx context.Context, db audit.DBPool, actorID, action, targetType, targetID string, details any) error {
	if db == nil {
		return errors.New("audit: database not configured")
	}
	return audit.WriteEntry(ctx, db, actorID, action, targetType, targetID, details)
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
func (h *Handler) writeAuditEntryBestEffort(actorID, action, targetType, targetID string, details any) error {
	if h.db == nil {
		return errors.New("audit: database not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
	defer cancel()
	return h.writeAuditEntry(ctx, actorID, action, targetType, targetID, details)
}
