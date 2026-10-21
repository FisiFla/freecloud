package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeRow implements pgx.Row.
type fakeRow struct {
	scanFn func(dest ...any) error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.scanFn != nil {
		return r.scanFn(dest...)
	}
	return pgx.ErrNoRows
}

// fakeRows implements pgx.Rows backed by a slice of scanFns.
type fakeRows struct {
	scanFns []func(dest ...any) error
	idx     int
}

func (r *fakeRows) Next() bool                                   { return r.idx < len(r.scanFns) }
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]interface{}, error)               { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Scan(dest ...any) error {
	fn := r.scanFns[r.idx]
	r.idx++
	return fn(dest...)
}

// fakeDB implements audit.DBPool.
type fakeDB struct {
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
}

func (d *fakeDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if d.execFn != nil {
		return d.execFn(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, nil
}
func (d *fakeDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if d.queryFn != nil {
		return d.queryFn(ctx, sql, args...)
	}
	return nil, errors.New("fakeDB.Query not wired")
}
func (d *fakeDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if d.queryRowFn != nil {
		return d.queryRowFn(ctx, sql, args...)
	}
	return fakeRow{} // pgx.ErrNoRows
}
func (d *fakeDB) Begin(ctx context.Context) (pgx.Tx, error) {
	return &fakeTx{db: d}, nil
}

type fakeTx struct {
	db *fakeDB
}

func (tx *fakeTx) Begin(ctx context.Context) (pgx.Tx, error) { return tx, nil }
func (tx *fakeTx) Commit(ctx context.Context) error          { return nil }
func (tx *fakeTx) Rollback(ctx context.Context) error        { return nil }
func (tx *fakeTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (tx *fakeTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults { return nil }
func (tx *fakeTx) LargeObjects() pgx.LargeObjects                               { return pgx.LargeObjects{} }
func (tx *fakeTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (tx *fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return tx.db.Exec(ctx, sql, args...)
}
func (tx *fakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return tx.db.Query(ctx, sql, args...)
}
func (tx *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.db.QueryRow(ctx, sql, args...)
}
func (tx *fakeTx) Conn() *pgx.Conn { return nil }

// buildChain constructs a realistic sequence of entries (actorID, action, ...) and
// returns them as ChainEntry values with correct hashes computed in order.
//
// The hashes are computed by an INDEPENDENT implementation of the documented
// canonical form (see referenceHash below), NOT by the production computeHash
// helper. Using computeHash here made the test tautological: the same function
// produced the expected values and verified them, so a change to the hash
// format could not be detected (issue #228).
func buildChain(entries []ChainEntry) []ChainEntry {
	prev := ""
	out := make([]ChainEntry, len(entries))
	for i, e := range entries {
		e.PrevHash = prev
		e.RowHash = referenceHash(e.ActorID, e.Action, e.TargetType, e.TargetID, e.Details, prev)
		out[i] = e
		prev = e.RowHash
	}
	return out
}

// referenceHash is an independent implementation of the audit chain's
// canonical hash, written from the documented format rather than reusing the
// production helper:
//
//	hash = SHA-256( for each field: len(field) || ":" || field || "|" )
//
// with the fields in the order actorID, action, targetType, targetID,
// details, prevHash.
func referenceHash(actorID, action, targetType, targetID, details, prevHash string) string {
	fields := []string{actorID, action, targetType, targetID, details, prevHash}
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "%d:%s|", len(f), f)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func chainRows(entries []ChainEntry) pgx.Rows {
	fns := make([]func(dest ...any) error, len(entries))
	for i, e := range entries {
		e := e // capture
		fns[i] = func(dest ...any) error {
			// seq, id, actor_id, action, target_type, target_id, details, row_hash, prev_hash, created_at
			if len(dest) < 10 {
				return errors.New("not enough dest slots")
			}
			*dest[0].(*int64) = e.Seq
			*dest[1].(*string) = e.ID
			*dest[2].(*string) = e.ActorID
			*dest[3].(*string) = e.Action
			*dest[4].(*string) = e.TargetType
			*dest[5].(*string) = e.TargetID
			*dest[6].(*string) = e.Details
			*dest[7].(*string) = e.RowHash
			*dest[8].(*string) = e.PrevHash
			return nil // skip created_at (zero value ok for test)
		}
	}
	return &fakeRows{scanFns: fns}
}

func TestVerifyChainValid(t *testing.T) {
	raw := []ChainEntry{
		{Seq: 1, ID: "a", ActorID: "alice", Action: "onboard", TargetType: "user", TargetID: "u1", Details: "{}"},
		{Seq: 2, ID: "b", ActorID: "bob", Action: "offboard", TargetType: "user", TargetID: "u2", Details: "{}"},
		{Seq: 3, ID: "c", ActorID: "alice", Action: "login", TargetType: "user", TargetID: "u1", Details: "{}"},
	}
	chain := buildChain(raw)

	db := &fakeDB{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return chainRows(chain), nil
		},
	}

	res, err := VerifyChain(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Errorf("expected OK=true, got false: %s", res.Error)
	}
	if res.RowsChecked != 3 {
		t.Errorf("expected 3 rows checked, got %d", res.RowsChecked)
	}
}

// TestVerifyChainDetectsWrongHashFormat proves the chain test is not
// tautological: a chain whose hashes were computed with a DIFFERENT canonical
// form must be rejected. Before the fix the test built its expectations with
// the very helper it was verifying, so a change to the hash format (or to the
// fields that go into it) could not be detected (issue #228).
func TestVerifyChainDetectsWrongHashFormat(t *testing.T) {
	raw := []ChainEntry{
		{Seq: 1, ID: "a", ActorID: "alice", Action: "onboard", TargetType: "user", TargetID: "u1", Details: "{}"},
	}
	// Build the chain with a DIFFERENT (wrong) canonical form: the fields
	// are joined without the length prefix.
	chain := buildChainWrongFormat(raw)

	db := &fakeDB{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return chainRows(chain), nil
		},
	}

	res, err := VerifyChain(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Error("expected a chain built with the wrong hash format to be rejected, got OK=true")
	}
	if res.FirstBreakSeq != 1 {
		t.Errorf("expected the break at seq 1, got %d", res.FirstBreakSeq)
	}
}

// buildChainWrongFormat builds a chain whose hashes use a different canonical
// form (plain concatenation instead of length-prefixed fields).
func buildChainWrongFormat(entries []ChainEntry) []ChainEntry {
	prev := ""
	out := make([]ChainEntry, len(entries))
	for i, e := range entries {
		e.PrevHash = prev
		sum := sha256.Sum256([]byte(e.ActorID + e.Action + e.TargetType + e.TargetID + e.Details + prev))
		e.RowHash = hex.EncodeToString(sum[:])
		out[i] = e
		prev = e.RowHash
	}
	return out
}

// TestVerifyChainMultiKeyDetails proves the chain verifies a row whose
// details are a multi-key JSON object — the shape the writer actually stores.
// Every previous fixture used the single-key value "{}", which is the one
// value where Go's json.Marshal and Postgres' jsonb::text agree, so the
// fixtures never exercised the canonicalisation (issue #228).
func TestVerifyChainMultiKeyDetails(t *testing.T) {
	details := `{"device_ids": ["d1", "d2"], "email": "alice@tenant-b.com", "role": "engineer"}`
	raw := []ChainEntry{
		{Seq: 1, ID: "a", ActorID: "alice", Action: "onboard", TargetType: "user", TargetID: "u1", Details: details},
		{Seq: 2, ID: "b", ActorID: "bob", Action: "offboard", TargetType: "user", TargetID: "u2", Details: details},
	}
	chain := buildChain(raw)

	db := &fakeDB{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return chainRows(chain), nil
		},
	}

	res, err := VerifyChain(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Errorf("expected OK=true for multi-key details, got false: %s", res.Error)
	}
}

func TestVerifyChainTamperedRowHash(t *testing.T) {
	raw := []ChainEntry{
		{Seq: 1, ID: "a", ActorID: "alice", Action: "onboard", TargetType: "user", TargetID: "u1", Details: "{}"},
		{Seq: 2, ID: "b", ActorID: "bob", Action: "offboard", TargetType: "user", TargetID: "u2", Details: "{}"},
	}
	chain := buildChain(raw)
	// Tamper: alter the action of row 2 in memory (hash stays the same → mismatch)
	chain[1].Action = "tampered"

	db := &fakeDB{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return chainRows(chain), nil
		},
	}

	res, err := VerifyChain(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Error("expected OK=false for tampered row, got true")
	}
	if res.FirstBreakSeq != 2 {
		t.Errorf("expected break at seq=2, got %d", res.FirstBreakSeq)
	}
}

func TestVerifyChainDeletedMiddleRow(t *testing.T) {
	raw := []ChainEntry{
		{Seq: 1, ID: "a", ActorID: "alice", Action: "onboard", TargetType: "user", TargetID: "u1", Details: "{}"},
		{Seq: 2, ID: "b", ActorID: "bob", Action: "offboard", TargetType: "user", TargetID: "u2", Details: "{}"},
		{Seq: 3, ID: "c", ActorID: "alice", Action: "login", TargetType: "user", TargetID: "u1", Details: "{}"},
	}
	chain := buildChain(raw)
	// Simulate deletion of row 2 by presenting rows [1, 3] to VerifyChain.
	// Row 3's prev_hash still points at row 2's hash → mismatch with row 1's hash.
	truncated := []ChainEntry{chain[0], chain[2]}

	db := &fakeDB{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return chainRows(truncated), nil
		},
	}

	res, err := VerifyChain(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Error("expected OK=false when a row is deleted, got true")
	}
	if res.FirstBreakSeq != 3 {
		t.Errorf("expected break at seq=3, got %d", res.FirstBreakSeq)
	}
}

func TestVerifyChainPrunedPrefixWithAnchor(t *testing.T) {
	raw := []ChainEntry{
		{Seq: 1, ID: "a", ActorID: "alice", Action: "onboard", TargetType: "user", TargetID: "u1", Details: "{}"},
		{Seq: 2, ID: "b", ActorID: "bob", Action: "offboard", TargetType: "user", TargetID: "u2", Details: "{}"},
		{Seq: 3, ID: "c", ActorID: "alice", Action: "login", TargetType: "user", TargetID: "u1", Details: "{}"},
	}
	chain := buildChain(raw)
	retained := []ChainEntry{chain[1], chain[2]}

	db := &fakeDB{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return chainRows(retained), nil
		},
		queryRowFn: func(_ context.Context, _ string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*dest[0].(*int64) = retained[0].Seq
				*dest[1].(*string) = retained[0].PrevHash
				return nil
			}}
		},
	}

	res, err := VerifyChain(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK=true for anchored retained chain, got false: %s", res.Error)
	}
}

func TestVerifyChainEmpty(t *testing.T) {
	db := &fakeDB{
		queryFn: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
			return &fakeRows{}, nil
		},
	}
	res, err := VerifyChain(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Errorf("empty chain should be OK, got false: %s", res.Error)
	}
}
