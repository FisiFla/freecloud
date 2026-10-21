package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// countingFleetDB counts the fleet_config queries a call makes, so a test can
// prove the client is cached instead of rebuilt per request.
type countingFleetDB struct {
	queries int
	err     error
}

func (d *countingFleetDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	d.queries++
	if d.err != nil {
		return fakeRow{scanFn: func(dest ...any) error { return d.err }}
	}
	return fakeRow{scanFn: func(dest ...any) error {
		*dest[0].(*string) = "https://fleet.example.com"
		if dest[1].(**string) != nil {
			enc := "encrypted-token"
			*dest[1].(**string) = &enc
		}
		return nil
	}}
}

func (d *countingFleetDB) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d *countingFleetDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("countingFleetDB.Query not implemented")
}

func (d *countingFleetDB) Begin(_ context.Context) (pgx.Tx, error) {
	return nil, errors.New("countingFleetDB.Begin not implemented")
}

// TestDynamicFleetClientCachesClient proves the Fleet client is built once
// and reused: every call previously re-queried fleet_config, re-decrypted the
// AES token and built a new http.Client (and therefore a new connection
// pool) — issue #177.
func TestDynamicFleetClientCachesClient(t *testing.T) {
	db := &countingFleetDB{}
	d := NewDynamicFleetClient(db, "https://fallback.example.com", "fallback-token", zap.NewNop()).(*dynamicFleetClient)

	first := d.client(context.Background())
	second := d.client(context.Background())
	third := d.client(context.Background())

	if first != second || second != third {
		t.Error("expected the same cached Fleet client to be reused across calls")
	}
	if db.queries != 1 {
		t.Errorf("expected exactly 1 fleet_config query, got %d", db.queries)
	}
}

// TestDynamicFleetClientRebuildsAfterTTL proves the cache expires so a
// configuration change (new server URL or API token) is picked up.
func TestDynamicFleetClientRebuildsAfterTTL(t *testing.T) {
	db := &countingFleetDB{}
	d := NewDynamicFleetClient(db, "https://fallback.example.com", "fallback-token", zap.NewNop()).(*dynamicFleetClient)

	d.client(context.Background())
	// Age the cache past its TTL.
	d.mu.Lock()
	d.cachedAt = time.Now().Add(-fleetClientCacheTTL - time.Second)
	d.mu.Unlock()

	d.client(context.Background())

	if db.queries != 2 {
		t.Errorf("expected the client to be rebuilt after the TTL, got %d queries", db.queries)
	}
}

// TestDynamicFleetClientFallsBackOnQueryError proves a failing config query
// still yields a usable client (the env fallback), and that the failure does
// not break the request path.
func TestDynamicFleetClientFallsBackOnQueryError(t *testing.T) {
	db := &countingFleetDB{err: errors.New("db down")}
	d := NewDynamicFleetClient(db, "https://fallback.example.com", "fallback-token", zap.NewNop()).(*dynamicFleetClient)

	c := d.client(context.Background())
	if c == nil {
		t.Fatal("expected a client even when the config query fails")
	}
}

// TestDynamicFleetClientUsesFallbackWhenNoRow proves the env fallback is used
// when no fleet_config row exists yet.
func TestDynamicFleetClientUsesFallbackWhenNoRow(t *testing.T) {
	db := &countingFleetDB{err: pgx.ErrNoRows}
	d := NewDynamicFleetClient(db, "https://fallback.example.com", "fallback-token", zap.NewNop()).(*dynamicFleetClient)

	if d.client(context.Background()) == nil {
		t.Fatal("expected a client")
	}
}
