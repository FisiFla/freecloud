// Package audit provides the hash-chaining layer for audit log integrity (C1).
// Each row's SHA-256 hash covers its canonical content plus the previous row's
// hash, forming an append-only chain anchored at seq order.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBPool is the subset of *pgxpool.Pool used here.
type DBPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

const auditChainLockID int64 = 0x4652434c4f5544 // "FRCLOUD" within signed int64 range.

// WriteEntry inserts one audit log row and computes row_hash + prev_hash in the
// same database round-trip. It reads the max-seq row's hash first, then inserts
// with a hash computed over the canonical fields + prevHash.
// actorID, action, targetType, targetID must be stable strings; details must be
// a JSON-marshalable value (use nil for no details).
func WriteEntry(ctx context.Context, db DBPool, actorID, action, targetType, targetID string, details interface{}) error {
	var detailsBytes []byte
	if details != nil {
		var err error
		detailsBytes, err = json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshal audit details: %w", err)
		}
	} else {
		detailsBytes = []byte("{}")
	}

	// When db is a pgx.Tx (called from within a caller's transaction), Begin
	// creates a SAVEPOINT rather than a real transaction. In that case the
	// pg_advisory_xact_lock below is held by the OUTER transaction and is
	// released at its commit or rollback — this is intentional and safe, because
	// the chain must remain consistent for the full duration of the enclosing
	// transaction, not just the inner SAVEPOINT.
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin audit write transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auditChainLockID); err != nil {
		return fmt.Errorf("lock audit chain: %w", err)
	}

	// Read the hash of the current chain head (the row with the highest seq).
	prevHash, err := chainHead(ctx, tx)
	if err != nil {
		return fmt.Errorf("read chain head: %w", err)
	}

	rowHash := computeHash(actorID, action, targetType, targetID, string(detailsBytes), prevHash)

	_, err = tx.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details, row_hash, prev_hash)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		actorID, action, targetType, targetID, detailsBytes, rowHash, prevHash,
	)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// chainHead returns the row_hash of the highest-seq row (the chain tip), or an
// empty string if no rows exist yet.
func chainHead(ctx context.Context, db DBPool) (string, error) {
	var h *string
	err := db.QueryRow(ctx,
		`SELECT row_hash FROM audit_logs ORDER BY seq DESC LIMIT 1`,
	).Scan(&h)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if h == nil {
		return "", nil
	}
	return *h, nil
}

// computeHash returns the hex-encoded SHA-256 over the canonical representation
// of one row plus the previous hash.
func computeHash(actorID, action, targetType, targetID, detailsJSON, prevHash string) string {
	h := sha256.New()
	// Fixed-length field separator to prevent length-extension confusion.
	for _, s := range []string{actorID, action, targetType, targetID, detailsJSON, prevHash} {
		fmt.Fprintf(h, "%d:%s|", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ChainEntry is one row as returned by VerifyChain.
type ChainEntry struct {
	Seq        int64
	ID         string
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	Details    string
	RowHash    string
	PrevHash   string
	CreatedAt  time.Time
}

// VerifyResult summarises a chain walk.
type VerifyResult struct {
	// OK is true when the entire chain is intact.
	OK bool `json:"ok"`
	// RowsChecked is the number of rows examined.
	RowsChecked int `json:"rowsChecked"`
	// FirstBreakSeq is the seq of the first broken link, or 0 if none.
	FirstBreakSeq int64 `json:"firstBreakSeq,omitempty"`
	// Error holds a human-readable description of the first break.
	Error string `json:"error,omitempty"`
}

// VerifyChain walks all rows in seq order and returns the result of the
// integrity check. It stops at the first broken link.
func VerifyChain(ctx context.Context, db DBPool) (VerifyResult, error) {
	anchorSeq, anchorPrevHash, err := chainAnchor(ctx, db)
	if err != nil {
		return VerifyResult{}, err
	}

	rows, err := db.Query(ctx,
		`SELECT seq, id, actor_id, action,
		        COALESCE(target_type, ''), COALESCE(target_id, ''),
		        COALESCE(details::text, '{}'),
		        COALESCE(row_hash, ''), COALESCE(prev_hash, ''),
		        created_at
		 FROM audit_logs
		 ORDER BY seq ASC`,
	)
	if err != nil {
		return VerifyResult{}, err
	}
	defer rows.Close()

	var result VerifyResult
	prevHash := ""

	for rows.Next() {
		var e ChainEntry
		if err := rows.Scan(
			&e.Seq, &e.ID, &e.ActorID, &e.Action,
			&e.TargetType, &e.TargetID, &e.Details,
			&e.RowHash, &e.PrevHash, &e.CreatedAt,
		); err != nil {
			return result, fmt.Errorf("scan row: %w", err)
		}
		result.RowsChecked++

		if result.RowsChecked == 1 && anchorSeq > 0 {
			if e.Seq != anchorSeq {
				result.FirstBreakSeq = e.Seq
				result.Error = fmt.Sprintf("seq %d: anchor mismatch (expected first retained seq %d)", e.Seq, anchorSeq)
				return result, nil
			}
			prevHash = anchorPrevHash
		}

		if e.RowHash == "" {
			result.FirstBreakSeq = e.Seq
			result.Error = fmt.Sprintf("seq %d: missing row_hash", e.Seq)
			return result, nil
		}

		if e.PrevHash != prevHash {
			result.FirstBreakSeq = e.Seq
			result.Error = fmt.Sprintf("seq %d: prev_hash mismatch (expected %q, got %q)", e.Seq, prevHash, e.PrevHash)
			return result, nil
		}

		expected := computeHash(e.ActorID, e.Action, e.TargetType, e.TargetID, e.Details, e.PrevHash)
		if e.RowHash != expected {
			result.FirstBreakSeq = e.Seq
			result.Error = fmt.Sprintf("seq %d: row_hash mismatch (expected %q, got %q)", e.Seq, expected, e.RowHash)
			return result, nil
		}

		prevHash = e.RowHash
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	result.OK = true
	return result, nil
}

func chainAnchor(ctx context.Context, db DBPool) (int64, string, error) {
	var seq int64
	var prevHash string
	err := db.QueryRow(ctx,
		`SELECT first_seq, prev_hash FROM audit_chain_anchors WHERE id = 1`,
	).Scan(&seq, &prevHash)
	if err == pgx.ErrNoRows {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	return seq, prevHash, nil
}
