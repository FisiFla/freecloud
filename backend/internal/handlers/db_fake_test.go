package handlers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeRow is a minimal pgx.Row whose Scan delegates to a function.
type fakeRow struct {
	scanFn func(dest ...any) error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.scanFn != nil {
		return r.scanFn(dest...)
	}
	return nil
}

// fakeQueryRows is a minimal pgx.Rows whose Next/Scan are backed by a
// pre-populated slice. Tests use it to exercise the Query → rows.Next loop.
type fakeQueryRows struct {
	rows [][]interface{}
	idx  int
	err  error
}

func (r *fakeQueryRows) Next() bool                                   { return r.idx < len(r.rows) }
func (r *fakeQueryRows) Err() error                                   { return r.err }
func (r *fakeQueryRows) Close()                                       {}
func (r *fakeQueryRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeQueryRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeQueryRows) Values() ([]interface{}, error)               { return nil, nil }
func (r *fakeQueryRows) RawValues() [][]byte                          { return nil }
func (r *fakeQueryRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeQueryRows) Scan(dest ...any) error {
	if r.idx >= len(r.rows) {
		return errors.New("no more rows")
	}
	row := r.rows[r.idx]
	r.idx++
	for i, d := range dest {
		if i >= len(row) {
			break
		}
		switch p := d.(type) {
		case *string:
			if v, ok := row[i].(string); ok {
				*p = v
			}
		case *bool:
			if v, ok := row[i].(bool); ok {
				*p = v
			}
		}
	}
	return nil
}

// fakeDB is a minimal DBPool for unit tests. A test wires only the methods it
// needs; the rest return safe defaults. QueryRow defaults to "no rows" and
// Begin defaults to a failure (so persistence-path tests exercise rollback).
type fakeDB struct {
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	beginFn    func(ctx context.Context) (pgx.Tx, error)
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
	return nil, errors.New("fakeDB.Query not implemented")
}

func (d *fakeDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if d.queryRowFn != nil {
		return d.queryRowFn(ctx, sql, args...)
	}
	return fakeRow{scanFn: func(dest ...any) error { return pgx.ErrNoRows }}
}

func (d *fakeDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if d.beginFn != nil {
		return d.beginFn(ctx)
	}
	return nil, errors.New("fakeDB.Begin not implemented")
}

type fakeTx struct {
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	commitFn   func(ctx context.Context) error
}

func (tx *fakeTx) Begin(ctx context.Context) (pgx.Tx, error) {
	return tx, nil
}

func (tx *fakeTx) Commit(ctx context.Context) error {
	if tx.commitFn != nil {
		return tx.commitFn(ctx)
	}
	return nil
}

func (tx *fakeTx) Rollback(ctx context.Context) error {
	return nil
}

func (tx *fakeTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("fakeTx.CopyFrom not implemented")
}

func (tx *fakeTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *fakeTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *fakeTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("fakeTx.Prepare not implemented")
}

func (tx *fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx.execFn != nil {
		return tx.execFn(ctx, sql, args...)
	}
	return pgconn.CommandTag{}, nil
}

func (tx *fakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, errors.New("fakeTx.Query not implemented")
}

func (tx *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx.queryRowFn != nil {
		return tx.queryRowFn(ctx, sql, args...)
	}
	return fakeRow{}
}

func (tx *fakeTx) Conn() *pgx.Conn {
	return nil
}
