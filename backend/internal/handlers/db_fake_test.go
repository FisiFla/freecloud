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

// fakeDB is a minimal DBPool for unit tests. A test wires only the methods it
// needs; the rest return safe defaults. QueryRow defaults to "no rows" and
// Begin defaults to a failure (so persistence-path tests exercise rollback).
type fakeDB struct {
	execFn     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
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
