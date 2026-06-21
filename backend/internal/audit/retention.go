package audit

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Pruner periodically deletes audit rows older than a configured window.
// Pruning preserves chain integrity by removing from the oldest end only
// (rows whose seq precedes the chain window). After pruning, the chain
// starts at the first surviving row; its prev_hash is treated as a
// "truncation anchor" by VerifyChain (pre-chain rows with empty row_hash
// are already skipped). The prune action is itself audited via WriteEntry.
type Pruner struct {
	pool   DBPool
	logger *zap.Logger
}

// NewPruner creates a Pruner.
func NewPruner(pool DBPool, logger *zap.Logger) *Pruner {
	return &Pruner{pool: pool, logger: logger}
}

// Prune deletes rows whose created_at is older than retainFor and returns the
// count of rows deleted. It writes one audit row recording the prune action.
// retainFor == 0 means "keep forever" (noop).
func (p *Pruner) Prune(ctx context.Context, retainFor time.Duration) (int64, error) {
	if retainFor <= 0 {
		return 0, nil
	}

	cutoff := time.Now().UTC().Add(-retainFor)

	tag, err := p.pool.Exec(ctx,
		`DELETE FROM audit_logs WHERE created_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("prune audit_logs: %w", err)
	}
	deleted := tag.RowsAffected()

	if deleted > 0 {
		p.logger.Info("audit log pruned",
			zap.Int64("rows_deleted", deleted),
			zap.Time("cutoff", cutoff),
		)
		// Audit the prune itself — detached context so a caller cancellation
		// does not silently drop the record of this privileged action.
		auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = WriteEntry(auditCtx, p.pool, "system", "audit.prune", "audit_logs", "",
			map[string]interface{}{
				"rows_deleted": deleted,
				"cutoff":       cutoff.Format(time.RFC3339),
			},
		)
	}
	return deleted, nil
}

// Start launches the periodic prune ticker.
// interval == 0 disables it.
// retainFor == 0 means keep forever (the ticker runs but Prune is a noop).
func (p *Pruner) Start(ctx context.Context, interval, retainFor time.Duration) {
	if interval <= 0 {
		p.logger.Info("audit retention job disabled (AUDIT_PRUNE_INTERVAL=0)")
		return
	}
	p.logger.Info("audit retention job started",
		zap.Duration("interval", interval),
		zap.Duration("retain_for", retainFor),
	)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				p.logger.Info("audit retention job stopped")
				return
			case <-ticker.C:
				if _, err := p.Prune(ctx, retainFor); err != nil {
					p.logger.Warn("audit prune failed", zap.Error(err))
				}
			}
		}
	}()
}
