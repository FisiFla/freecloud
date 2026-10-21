package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This file holds the round-trip test for the audit hash chain.
//
// The chain used to be verified against a hash computed with the very same
// helper that produced it, so the test could not tell a correct writer from a
// broken one. The writer hashes the exact text Postgres stores for a row
// (details::text), while Go's json.Marshal produces a different byte
// sequence (different key order, different spacing, HTML-escaped). The
// memAuditDB fake below models Postgres' jsonb output faithfully so that
// WriteEntry and VerifyChain are exercised against each other.

// postgresJSONBText renders raw JSON the way Postgres' jsonb output does:
// object keys ordered by length then bytewise, ": " and ", " separators and no
// HTML escaping. Numbers keep their literal form.
func postgresJSONBText(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "{}"
	}
	return renderJSONBValue(v)
}

func renderJSONBValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case json.Number:
		return x.String()
	case string:
		return encodeJSONBString(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = renderJSONBValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if len(keys[i]) != len(keys[j]) {
				return len(keys[i]) < len(keys[j])
			}
			return keys[i] < keys[j]
		})
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = encodeJSONBString(k) + ": " + renderJSONBValue(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return "null"
	}
}

// encodeJSONBString encodes a JSON string without Go's HTML escaping, which
// Postgres does not apply.
func encodeJSONBString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return `""`
	}
	return strings.TrimRight(buf.String(), "\n")
}

// memAuditDB is a stateful in-memory audit.DBPool. It stores the rows that
// WriteEntry inserts and serves them back to VerifyChain the way Postgres
// would.
type memAuditDB struct {
	rows   []ChainEntry
	orgIDs []string // org_id of each inserted row, in insertion order
	seq    int64
}

func (d *memAuditDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "INSERT INTO audit_logs"):
		d.seq++
		raw, _ := args[5].([]byte)
		d.orgIDs = append(d.orgIDs, args[0].(string))
		d.rows = append(d.rows, ChainEntry{
			Seq:        d.seq,
			ID:         fmt.Sprintf("row-%d", d.seq),
			ActorID:    args[1].(string),
			Action:     args[2].(string),
			TargetType: args[3].(string),
			TargetID:   args[4].(string),
			// Postgres stores the jsonb value and renders it back on read.
			Details:  postgresJSONBText(raw),
			RowHash:  args[6].(string),
			PrevHash: args[7].(string),
		})
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	default:
		return pgconn.CommandTag{}, nil
	}
}

func (d *memAuditDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if !strings.Contains(sql, "FROM audit_logs") {
		return nil, nil
	}
	fns := make([]func(dest ...any) error, len(d.rows))
	for i, e := range d.rows {
		e := e
		fns[i] = func(dest ...any) error {
			*dest[0].(*int64) = e.Seq
			*dest[1].(*string) = e.ID
			*dest[2].(*string) = e.ActorID
			*dest[3].(*string) = e.Action
			*dest[4].(*string) = e.TargetType
			*dest[5].(*string) = e.TargetID
			*dest[6].(*string) = e.Details
			*dest[7].(*string) = e.RowHash
			*dest[8].(*string) = e.PrevHash
			*dest[9].(*time.Time) = time.Now()
			return nil
		}
	}
	return &fakeRows{scanFns: fns}, nil
}

func (d *memAuditDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "::jsonb)::text"):
		raw, _ := args[0].([]byte)
		return fakeRow{scanFn: func(dest ...any) error {
			*dest[0].(*string) = postgresJSONBText(raw)
			return nil
		}}
	case strings.Contains(sql, "FROM audit_logs ORDER BY seq DESC"):
		if len(d.rows) == 0 {
			return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
		}
		head := d.rows[len(d.rows)-1].RowHash
		return fakeRow{scanFn: func(dest ...any) error {
			// chainHead scans into a *string, so the destination is **string.
			*dest[0].(**string) = &head
			return nil
		}}
	case strings.Contains(sql, "FROM audit_chain_anchors"):
		return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
	default:
		return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
	}
}

func (d *memAuditDB) Begin(_ context.Context) (pgx.Tx, error) { return &memAuditTx{db: d}, nil }

type memAuditTx struct{ db *memAuditDB }

func (t *memAuditTx) Begin(_ context.Context) (pgx.Tx, error) { return t, nil }
func (t *memAuditTx) Commit(_ context.Context) error          { return nil }
func (t *memAuditTx) Rollback(_ context.Context) error        { return nil }
func (t *memAuditTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (t *memAuditTx) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults { return nil }
func (t *memAuditTx) LargeObjects() pgx.LargeObjects                             { return pgx.LargeObjects{} }
func (t *memAuditTx) Prepare(_ context.Context, _ string, _ string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (t *memAuditTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.db.Exec(ctx, sql, args...)
}
func (t *memAuditTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.db.Query(ctx, sql, args...)
}
func (t *memAuditTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.db.QueryRow(ctx, sql, args...)
}
func (t *memAuditTx) Conn() *pgx.Conn { return nil }

func itoa(n int64) string { return fmt.Sprintf("%d", n) }

// TestWriteEntryThenVerifyChainRoundTrip is the regression test for the
// broken hash chain: a row written by WriteEntry must verify, even when its
// details contain several keys, characters that Go's JSON encoder escapes
// differently from Postgres, and keys whose sort order differs between the
// two implementations.
func TestWriteEntryThenVerifyChainRoundTrip(t *testing.T) {
	db := &memAuditDB{}
	ctx := context.Background()

	details := map[string]any{
		"email":      "alice@tenant-b.com",
		"role":       "engineer",
		"device_ids": []string{"d1", "d2"},
		// '&' and '<' are HTML-escaped by Go's json.Marshal but not by
		// Postgres' jsonb output.
		"error": "a & b < c",
	}

	if err := WriteEntry(ctx, db, "11111111-1111-1111-1111-111111111111", "alice@tenant-b.com",
		"onboard", "user", "u-1", details); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}
	if err := WriteEntry(ctx, db, "11111111-1111-1111-1111-111111111111", "bob@tenant-b.com",
		"offboard", "user", "u-2", map[string]any{"ok": true}); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}

	res, err := VerifyChain(ctx, db)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected chain to verify, got OK=false at seq %d: %s", res.FirstBreakSeq, res.Error)
	}
	if res.RowsChecked != 2 {
		t.Errorf("expected 2 rows checked, got %d", res.RowsChecked)
	}
}

// TestWriteEntryStoresOrgID proves the writer attributes the row to the
// caller's organization rather than to the Default Organization.
func TestWriteEntryStoresOrgID(t *testing.T) {
	db := &memAuditDB{}
	const orgID = "11111111-1111-1111-1111-111111111111"

	if err := WriteEntry(context.Background(), db, orgID, "alice", "onboard", "user", "u-1", nil); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}
	if len(db.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(db.rows))
	}
	if db.orgIDs[0] != orgID {
		t.Errorf("expected org_id %s, got %q", orgID, db.orgIDs[0])
	}
}

// TestWriteEntryRequiresOrgID keeps the writer fail-closed: an audit row
// without a tenant is never written.
func TestWriteEntryRequiresOrgID(t *testing.T) {
	db := &memAuditDB{}
	if err := WriteEntry(context.Background(), db, "", "alice", "onboard", "user", "u-1", nil); err == nil {
		t.Fatal("expected an error when org id is empty")
	}
	if len(db.rows) != 0 {
		t.Errorf("expected no rows written, got %d", len(db.rows))
	}
}

// fakeCanonicalRow answers the audit writer's canonicalisation query
// ("SELECT ($1::jsonb)::text") the way Postgres would.
func fakeCanonicalRow(args []any) fakeRow {
	raw, _ := args[0].([]byte)
	return fakeRow{scanFn: func(dest ...any) error {
		*dest[0].(*string) = postgresJSONBText(raw)
		return nil
	}}
}
