//go:build integration

package handlers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FisiFla/freecloud/backend/internal/db"
)

// TestMFARecoveryCodesLookupIndex proves the (user_id, code_hash) index on
// mfa_recovery_codes exists after the migrations run.
//
// CheckAndConsumeRecoveryCode looks a code up by (user_id, code_hash) on
// every recovery attempt. Without an index on those columns Postgres
// full-scans mfa_recovery_codes per attempt, so a handful of guesses costs a
// table scan (issue #170).
func TestMFARecoveryCodesLookupIndex(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	defer pool.Close()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var exists bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT FROM pg_indexes WHERE indexname = 'idx_mfa_recovery_codes_user_code_hash')`,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check index: %v", err)
	}
	if !exists {
		t.Error("expected index idx_mfa_recovery_codes_user_code_hash to exist after migrations")
	}
}

// TestAuditLogOrgIsolationRoundTrip proves an audit row keeps the org it was
// written for, and that a query filtered by org only returns that org's rows.
//
// The audit_logs table carries a single global hash chain (ADR 0005), so
// org_id is a filter column: writing a row without the caller's org would
// file it under the wrong tenant (issue #228).
func TestAuditLogOrgIsolationRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	defer pool.Close()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	trunc := `TRUNCATE audit_logs RESTART IDENTITY CASCADE`
	_, _ = pool.Exec(ctx, trunc)
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(c, trunc)
	})

	const orgA = "10000000-0000-0000-0000-00000000000a"
	const orgB = "20000000-0000-0000-0000-00000000000b"

	// The orgs must exist: audit_logs.org_id references organizations(id).
	// Only the two test orgs are removed afterwards — the seeded Default
	// Organization must survive for the other tests.
	for _, orgID := range []string{orgA, orgB} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`,
			orgID, "Org "+orgID[:1], "org-"+orgID[:1],
		); err != nil {
			t.Fatalf("insert org %s: %v", orgID, err)
		}
	}
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(c, `DELETE FROM organizations WHERE id = $1 OR id = $2`, orgA, orgB)
	})

	// Write one row per org, explicitly naming the org.
	for _, orgID := range []string{orgA, orgB} {
		_, err := pool.Exec(ctx,
			`INSERT INTO audit_logs (org_id, actor_id, action, target_type, target_id, details)
			 VALUES ($1, $2, 'onboard', 'user', 'u1', $3)`,
			orgID, "actor-"+orgID[:1], `{"email":"x@example.com"}`,
		)
		if err != nil {
			t.Fatalf("insert audit log for org %s: %v", orgID, err)
		}
	}

	// A query filtered by org must return ONLY that org's row.
	for _, orgID := range []string{orgA, orgB} {
		rows, err := pool.Query(ctx,
			`SELECT actor_id FROM audit_logs WHERE org_id = $1`, orgID)
		if err != nil {
			t.Fatalf("query audit logs for org %s: %v", orgID, err)
		}
		var actors []string
		for rows.Next() {
			var actor string
			if err := rows.Scan(&actor); err != nil {
				rows.Close()
				t.Fatalf("scan audit log: %v", err)
			}
			actors = append(actors, actor)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate audit logs: %v", err)
		}
		if len(actors) != 1 {
			t.Errorf("org %s: expected exactly 1 row, got %d (%v)", orgID, len(actors), actors)
			continue
		}
		if actors[0] != "actor-"+orgID[:1] {
			t.Errorf("org %s: got another org's row: %q", orgID, actors[0])
		}
	}
}

// TestAuditLogDefaultOrgIsApplied proves an audit row written without an
// explicit org is filed under the Default Organization rather than being
// rejected or filed under an arbitrary tenant.
func TestAuditLogDefaultOrgIsApplied(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	defer pool.Close()
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	trunc := `TRUNCATE audit_logs RESTART IDENTITY CASCADE`
	_, _ = pool.Exec(ctx, trunc)
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(c, trunc)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ('admin-1', 'onboard', 'user', 'u1', '{}')`); err != nil {
		t.Fatalf("insert audit log without an explicit org: %v", err)
	}

	var orgID string
	if err := pool.QueryRow(ctx, `SELECT org_id::TEXT FROM audit_logs LIMIT 1`).Scan(&orgID); err != nil {
		t.Fatalf("read org_id: %v", err)
	}
	if orgID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("expected the Default Organization, got %q", orgID)
	}
}
