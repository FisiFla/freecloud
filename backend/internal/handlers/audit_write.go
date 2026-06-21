package handlers

import (
	"context"
	"time"

	"github.com/FisiFla/freecloud/backend/internal/audit"
)

const auditWriteTimeout = 5 * time.Second

func writeAuditEntry(ctx context.Context, db audit.DBPool, actorID, action, targetType, targetID string, details any) error {
	if db == nil {
		return nil
	}
	return audit.WriteEntry(ctx, db, actorID, action, targetType, targetID, details)
}

func (h *Handler) writeAuditEntry(ctx context.Context, actorID, action, targetType, targetID string, details any) error {
	if h.db == nil {
		return nil
	}
	return writeAuditEntry(ctx, h.db, actorID, action, targetType, targetID, details)
}

func (h *Handler) writeAuditEntryDetached(actorID, action, targetType, targetID string, details any) error {
	if h.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
	defer cancel()
	return h.writeAuditEntry(ctx, actorID, action, targetType, targetID, details)
}
