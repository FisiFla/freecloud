package fleetteams

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to TEST_DATABASE_URL. Skips when unset (local unit-only runs).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed fleetteams tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ensureFleetTeamOrgsTable creates the target table if absent so the test can
// run against a plain postgres (CI db-integration job has it via migrations).
func ensureFleetTeamOrgsTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS fleet_team_orgs (
		fleet_team_id BIGINT PRIMARY KEY,
		org_id TEXT NOT NULL,
		team_name TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatalf("ensure table: %v", err)
	}
}

func TestApplyMappingsTx_InsertsAndUpserts(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ensureFleetTeamOrgsTable(t, pool)

	// Clean slate for deterministic assertions.
	if _, err := pool.Exec(ctx, `DELETE FROM fleet_team_orgs WHERE fleet_team_id IN (9001, 9002)`); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	rows := []MappingRow{
		{FleetTeamID: 9001, OrgID: "org-a", TeamName: "TeamA"},
		{FleetTeamID: 9002, OrgID: "org-b", TeamName: "TeamB"},
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	n, err := ApplyMappingsTx(ctx, tx, rows)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("apply: %v", err)
	}
	if n != 2 {
		tx.Rollback(ctx)
		t.Fatalf("expected 2 rows applied, got %d", n)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Verify persisted values.
	var orgID, name string
	if err := pool.QueryRow(ctx, `SELECT org_id, team_name FROM fleet_team_orgs WHERE fleet_team_id = 9001`).Scan(&orgID, &name); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if orgID != "org-a" || name != "TeamA" {
		t.Fatalf("unexpected row: %q %q", orgID, name)
	}

	// Upsert path: same fleet_team_id updates org/name.
	upsert := []MappingRow{{FleetTeamID: 9001, OrgID: "org-a2", TeamName: "TeamA2"}}
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin upsert: %v", err)
	}
	if _, err := ApplyMappingsTx(ctx, tx2, upsert); err != nil {
		tx2.Rollback(ctx)
		t.Fatalf("upsert apply: %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("upsert commit: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT org_id, team_name FROM fleet_team_orgs WHERE fleet_team_id = 9001`).Scan(&orgID, &name); err != nil {
		t.Fatalf("read back upsert: %v", err)
	}
	if orgID != "org-a2" || name != "TeamA2" {
		t.Fatalf("upsert not applied: %q %q", orgID, name)
	}
}

func TestApplyMappingsTx_RollsBackOnError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	ensureFleetTeamOrgsTable(t, pool)

	if _, err := pool.Exec(ctx, `DELETE FROM fleet_team_orgs WHERE fleet_team_id = 9003`); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// Second row forces a constraint error (NULL org_id) → whole tx must roll back.
	rows := []MappingRow{
		{FleetTeamID: 9003, OrgID: "org-c", TeamName: "TeamC"},
		{FleetTeamID: 9004, OrgID: "", TeamName: "Bad"},
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := ApplyMappingsTx(ctx, tx, rows); err == nil {
		tx.Rollback(ctx)
		t.Fatal("expected apply error on bad row")
	}
	tx.Rollback(ctx)

	// Nothing from the failed tx may have persisted.
	var cnt int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM fleet_team_orgs WHERE fleet_team_id = 9003`).Scan(&cnt); err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("rollback failed: %d rows persisted", cnt)
	}
}
