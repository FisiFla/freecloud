package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Pruner periodically deletes audit rows older than a configured window.
// Pruning records the first surviving row's prev_hash in audit_chain_anchors
// before deleting old rows, so VerifyChain can validate the retained suffix
// without pretending the pruned prefix still exists.
type Pruner struct {
	pool   DBPool
	logger *zap.Logger
	// isLeader gates the ticker-driven prune (B3, v1.7 HA): nil means "always
	// run" (single-instance / no leader election wired up), matching prior
	// behavior exactly.
	isLeader func() bool
}

// NewPruner creates a Pruner.
func NewPruner(pool DBPool, logger *zap.Logger) *Pruner {
	return &Pruner{pool: pool, logger: logger}
}

// SetLeaderGate wires a leader-election check (B3, v1.7 HA) so the ticker in
// Start only prunes on the instance that currently holds leadership.
func (p *Pruner) SetLeaderGate(isLeader func() bool) {
	p.isLeader = isLeader
}

// Prune deletes rows whose created_at is older than retainFor and returns the
// count of rows deleted. It writes one audit row recording the prune action.
// retainFor == 0 means "keep forever" (noop).
func (p *Pruner) Prune(ctx context.Context, retainFor time.Duration) (int64, error) {
	if retainFor <= 0 {
		return 0, nil
	}

	cutoff := time.Now().UTC().Add(-retainFor)

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin audit prune transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auditChainLockID); err != nil {
		return 0, fmt.Errorf("lock audit chain for prune: %w", err)
	}

	var anchorSeq int64
	anchorPrevHash := ""
	hasAnchor := false
	err = tx.QueryRow(ctx,
		`SELECT seq, prev_hash
		 FROM audit_logs
		 WHERE created_at >= $1
		 ORDER BY seq ASC
		 LIMIT 1`,
		cutoff,
	).Scan(&anchorSeq, &anchorPrevHash)
	if err == nil {
		hasAnchor = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("read audit prune anchor: %w", err)
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM audit_logs WHERE created_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("prune audit_logs: %w", err)
	}
	deleted := tag.RowsAffected()

	if deleted > 0 {
		if hasAnchor {
			if _, err := tx.Exec(ctx,
				`INSERT INTO audit_chain_anchors (id, first_seq, prev_hash, pruned_before, updated_at)
				 VALUES (1, $1, $2, $3, NOW())
				 ON CONFLICT (id) DO UPDATE SET
				   first_seq = EXCLUDED.first_seq,
				   prev_hash = EXCLUDED.prev_hash,
				   pruned_before = EXCLUDED.pruned_before,
				   updated_at = NOW()`,
				anchorSeq, anchorPrevHash, cutoff,
			); err != nil {
				return 0, fmt.Errorf("write audit prune anchor: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx,
				`DELETE FROM audit_chain_anchors WHERE id = 1`,
			); err != nil {
				return 0, fmt.Errorf("clear audit prune anchor: %w", err)
			}
		}

		p.logger.Info("audit log pruned",
			zap.Int64("rows_deleted", deleted),
			zap.Time("cutoff", cutoff),
		)
		if err := WriteEntry(ctx, tx, "system", "audit.prune", "audit_logs", "",
			map[string]interface{}{
				"rows_deleted": deleted,
				"cutoff":       cutoff.Format(time.RFC3339),
			},
		); err != nil {
			return 0, fmt.Errorf("write audit prune entry: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit audit prune: %w", err)
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
				if p.isLeader != nil && !p.isLeader() {
					continue
				}
				if _, err := p.Prune(ctx, retainFor); err != nil {
					p.logger.Warn("audit prune failed", zap.Error(err))
				}
			}
		}
	}()
}
