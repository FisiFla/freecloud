package audit

import (
	"context"
	"errors"
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
func buildChain(entries []ChainEntry) []ChainEntry {
	prev := ""
	out := make([]ChainEntry, len(entries))
	for i, e := range entries {
		e.PrevHash = prev
		e.RowHash = computeHash(e.ActorID, e.Action, e.TargetType, e.TargetID, e.Details, prev)
		out[i] = e
		prev = e.RowHash
	}
	return out
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
