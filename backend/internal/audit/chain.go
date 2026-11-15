// Package audit provides the hash-chaining layer for audit log integrity (C1).
// Each row's SHA-256 hash covers its canonical content plus the previous row's
// hash, forming an append-only chain anchored at seq order.
//
// The SHA-256 chain alone is unkeyed: someone with write access to audit_logs
// could modify a row and recompute every later hash. When a chain key is set
// (SetChainKey — production derives it from the provisioning master key),
// every new row also stores row_mac = HMAC-SHA256(key, row_hash), which
// cannot be recomputed without the server-side secret. VerifyChain then
// requires the MAC on the chain head and on every row after the first keyed
// one, so stripping MACs is detected too. Rows written before keying stay
// covered by the hash chain only. Not covered from the database alone:
// deleting rows from the tail (any earlier keyed row is a valid head). The
// external anchor for that is the SIEM stream, which ships every row's seq
// and row_hash off-host (internal/siem).
package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
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

type chainKeys struct{ current, previous []byte }

var chainKey atomic.Pointer[chainKeys]

// SetChainKey enables the keyed chain. current signs new rows; previous
// (optional) is accepted on verification only, for rows signed before a key
// rotation. A nil/empty current disables keying.
func SetChainKey(current, previous []byte) {
	if len(current) == 0 {
		chainKey.Store(nil)
		return
	}
	chainKey.Store(&chainKeys{current: current, previous: previous})
}

// DeriveChainKey derives the audit chain key from a master secret, so no
// separate secret has to be provisioned. Returns nil for an empty master.
func DeriveChainKey(master []byte) []byte {
	if len(master) == 0 {
		return nil
	}
	m := hmac.New(sha256.New, master)
	m.Write([]byte("freecloud-audit-chain-v1"))
	return m.Sum(nil)
}

func macHex(key []byte, rowHash string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(rowHash))
	return hex.EncodeToString(m.Sum(nil))
}

func (k *chainKeys) valid(rowHash, mac string) bool {
	if hmac.Equal([]byte(mac), []byte(macHex(k.current, rowHash))) {
		return true
	}
	return len(k.previous) > 0 && hmac.Equal([]byte(mac), []byte(macHex(k.previous, rowHash)))
}

// EnsureKeyedHead appends a marker entry when a chain key is set and the
// current head row carries no MAC (first start after keying was introduced),
// so that "the head is keyed" holds from then on and VerifyChain can treat
// an unkeyed head as tampering.
func EnsureKeyedHead(ctx context.Context, db DBPool) error {
	if chainKey.Load() == nil {
		return nil
	}
	var mac *string
	err := db.QueryRow(ctx, `SELECT row_mac FROM audit_logs ORDER BY seq DESC LIMIT 1`).Scan(&mac)
	if err == pgx.ErrNoRows || (err == nil && mac != nil && *mac != "") {
		return nil
	}
	if err != nil {
		return err
	}
	return WriteEntry(ctx, db, DefaultOrgID, "system", "audit.chain_keyed", "audit_chain", "", nil)
}

const auditChainLockID int64 = 0x4652434c4f5544 // "FRCLOUD" within signed int64 range.

// DefaultOrgID is the seeded "Default Organization" UUID used for audit rows
// that are not attributable to a specific tenant (for example the system
// prune marker). It mirrors middleware.DefaultOrgID; the audit package is a
// leaf and must not depend on the HTTP layer, so the constant is repeated
// here. chain_test.go asserts the two stay in sync.
const DefaultOrgID = "00000000-0000-0000-0000-000000000001"

// WriteEntry inserts one audit log row and computes row_hash + prev_hash in the
// same database round-trip. It reads the max-seq row's hash first, then inserts
// with a hash computed over the canonical fields + prevHash.
//
// orgID attributes the entry to a tenant. It is required: an audit row that
// cannot be attributed to an org would land in the Default Organization and be
// invisible to the tenant it describes.
//
// actorID, action, targetType, targetID must be stable strings; details must be
// a JSON-marshalable value (use nil for no details).
func WriteEntry(ctx context.Context, db DBPool, orgID, actorID, action, targetType, targetID string, details interface{}) error {
	if orgID == "" {
		return errors.New("audit: org id is required")
	}
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

	// Canonical details text.
	//
	// VerifyChain re-hashes the value it reads back from the database, which is
	// Postgres' own JSONB rendering (details::text). Go's json.Marshal output
	// is NOT byte-identical to that rendering — Postgres orders object keys by
	// length-then-byte, inserts a space after ':' and ',', and does not
	// HTML-escape. Hashing Go's bytes and verifying against Postgres' bytes
	// therefore never matched, which made every multi-key entry look like
	// tampering. Ask Postgres to render the exact value that is about to be
	// stored, so both sides hash the same bytes by construction. This also
	// matches the Migration028 backfill, which hashed details::text.
	var detailsText string
	if err := tx.QueryRow(ctx, `SELECT ($1::jsonb)::text`, detailsBytes).Scan(&detailsText); err != nil {
		return fmt.Errorf("canonicalise audit details: %w", err)
	}

	rowHash := computeHash(actorID, action, targetType, targetID, detailsText, prevHash)

	if k := chainKey.Load(); k != nil {
		_, err = tx.Exec(ctx,
			`INSERT INTO audit_logs (org_id, actor_id, action, target_type, target_id, details, row_hash, prev_hash, row_mac)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			orgID, actorID, action, targetType, targetID, detailsBytes, rowHash, prevHash, macHex(k.current, rowHash),
		)
	} else {
		_, err = tx.Exec(ctx,
			`INSERT INTO audit_logs (org_id, actor_id, action, target_type, target_id, details, row_hash, prev_hash)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			orgID, actorID, action, targetType, targetID, detailsBytes, rowHash, prevHash,
		)
	}
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
//
// detailsJSON must be the exact text that VerifyChain reads back for the row
// (i.e. Postgres' details::text rendering of the stored value). The writer
// obtains it from Postgres itself so the two sides can never disagree.
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
	// KeyedRows is the number of rows whose HMAC was verified (0 when no
	// chain key is configured).
	KeyedRows int `json:"keyedRows"`
}

// VerifyChain walks all rows in seq order and returns the result of the
// integrity check. It stops at the first broken link.
func VerifyChain(ctx context.Context, db DBPool) (VerifyResult, error) {
	anchorSeq, anchorPrevHash, err := chainAnchor(ctx, db)
	if err != nil {
		return VerifyResult{}, err
	}
	keys := chainKey.Load()
	macCol := "''"
	if keys != nil {
		macCol = "COALESCE(row_mac, '')"
	}

	rows, err := db.Query(ctx,
		`SELECT seq, id, actor_id, action,
		        COALESCE(target_type, ''), COALESCE(target_id, ''),
		        COALESCE(details::text, '{}'),
		        COALESCE(row_hash, ''), COALESCE(prev_hash, ''),
		        created_at, `+macCol+`
		 FROM audit_logs
		 ORDER BY seq ASC`,
	)
	if err != nil {
		return VerifyResult{}, err
	}
	defer rows.Close()

	var result VerifyResult
	prevHash := ""
	var lastSeq int64
	lastMac := ""

	for rows.Next() {
		var e ChainEntry
		var mac string
		if err := rows.Scan(
			&e.Seq, &e.ID, &e.ActorID, &e.Action,
			&e.TargetType, &e.TargetID, &e.Details,
			&e.RowHash, &e.PrevHash, &e.CreatedAt, &mac,
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

		if keys != nil {
			switch {
			case mac != "" && !keys.valid(e.RowHash, mac):
				result.FirstBreakSeq = e.Seq
				result.Error = fmt.Sprintf("seq %d: row_mac mismatch", e.Seq)
				return result, nil
			case mac == "" && result.KeyedRows > 0:
				result.FirstBreakSeq = e.Seq
				result.Error = fmt.Sprintf("seq %d: missing row_mac after keyed rows", e.Seq)
				return result, nil
			case mac != "":
				result.KeyedRows++
			}
		}
		lastSeq, lastMac = e.Seq, mac

		prevHash = e.RowHash
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if keys != nil && result.RowsChecked > 0 && lastMac == "" {
		result.FirstBreakSeq = lastSeq
		result.Error = fmt.Sprintf("seq %d: chain head carries no row_mac although a chain key is configured", lastSeq)
		return result, nil
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
