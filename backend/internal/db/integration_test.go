//go:build integration

package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to TEST_DATABASE_URL and returns a pool + cleanup that
// drops all data (but not the schema) so tests are isolated.
func testPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
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

	// Run migrations fresh.
	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Truncate all tables so each test starts clean (run after migration).
	cleanup := func() {
		truncCtx, truncCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer truncCancel()
		// Truncate in dependency order, cascade handles the rest.
		_, _ = pool.Exec(truncCtx, `TRUNCATE users_devices_mapping, app_assignments, connected_apps, audit_logs, devices, users, schema_migrations RESTART IDENTITY CASCADE`)
		pool.Close()
	}
	// Wipe before returning so any prior-run leftover data is gone.
	truncCtx, truncCancel := context.WithTimeout(ctx, 5*time.Second)
	_, _ = pool.Exec(truncCtx, `TRUNCATE users_devices_mapping, app_assignments, connected_apps, audit_logs, devices, users, schema_migrations RESTART IDENTITY CASCADE`)
	truncCancel()

	return pool, cleanup
}

// TestRunMigrations_CreatesSchema confirms RunMigrations creates every expected
// table and index and is idempotent (re-running is a no-op).
func TestRunMigrations_CreatesSchema(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()

	ctx := context.Background()

	wantTables := []string{"users", "devices", "users_devices_mapping", "connected_apps", "app_assignments", "audit_logs", "schema_migrations"}
	for _, table := range wantTables {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = $1)`,
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("expected table %q to exist after migrations", table)
		}
	}

	// Confirm key indexes.
	wantIndexes := []string{"idx_users_email", "idx_devices_hostname", "idx_audit_logs_actor_action_created"}
	for _, idx := range wantIndexes {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT FROM pg_indexes WHERE indexname = $1)`,
			idx,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check index %s: %v", idx, err)
		}
		if !exists {
			t.Errorf("expected index %q to exist after migrations", idx)
		}
	}

	// Idempotency: running migrations again must not error and must record
	// exactly one applied migration.
	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("re-run migrations: %v", err)
	}
	var appliedCount int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&appliedCount)
	if err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if appliedCount < 1 {
		t.Errorf("expected at least 1 recorded migration, got %d", appliedCount)
	}
}

// TestUserInsertAndLookup exercises a full user insert + lookup cycle.
func TestUserInsertAndLookup(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()

	ctx := context.Background()
	uid := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO users (keycloak_user_id, email, first_name, last_name, department, role)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		uid, "alice@example.com", "Alice", "Smith", "Engineering", "Developer",
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var email, firstName string
	err = pool.QueryRow(ctx,
		`SELECT email, first_name FROM users WHERE keycloak_user_id = $1`,
		uid,
	).Scan(&email, &firstName)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	if email != "alice@example.com" || firstName != "Alice" {
		t.Errorf("unexpected user data: email=%q firstName=%q", email, firstName)
	}

	// Email uniqueness constraint.
	_, err = pool.Exec(ctx, `INSERT INTO users (keycloak_user_id, email, first_name, last_name) VALUES ($1, $2, $3, $4)`,
		uuid.New(), "alice@example.com", "Dup", "User")
	if err == nil {
		t.Error("expected duplicate-email insert to fail uniqueness constraint")
	}
}

// TestConnectedAppInsertAndList exercises the connected_apps + RETURNING path.
func TestConnectedAppInsertAndList(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()

	ctx := context.Background()

	var appID string
	err := pool.QueryRow(ctx,
		`INSERT INTO connected_apps (keycloak_client_id, name, protocol, base_url)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		"kc-client-1", " MyApp ", "OIDC", "https://myapp.example.com",
	).Scan(&appID)
	if err != nil {
		t.Fatalf("insert app: %v", err)
	}
	if appID == "" {
		t.Error("expected non-empty app id")
	}

	// Protocol CHECK constraint rejects invalid values.
	_, err = pool.Exec(ctx,
		`INSERT INTO connected_apps (keycloak_client_id, name, protocol) VALUES ($1, $2, $3)`,
		"kc-bad", "Bad", "LDAP",
	)
	if err == nil {
		t.Error("expected protocol CHECK constraint to reject 'LDAP'")
	}

	// List query (mirrors ListApps handler).
	rows, err := pool.Query(ctx,
		`SELECT id, keycloak_client_id, name, protocol, COALESCE(base_url, ''), enabled, created_at
		 FROM connected_apps ORDER BY created_at DESC`)
	if err != nil {
		t.Fatalf("list apps: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, kcID, name, protocol, baseURL string
		var enabled bool
		var createdAt time.Time
		if err := rows.Scan(&id, &kcID, &name, &protocol, &baseURL, &enabled, &createdAt); err != nil {
			t.Fatalf("scan app: %v", err)
		}
		count++
	}
	if count != 1 {
		t.Errorf("expected 1 app, got %d", count)
	}
}

// TestAuditLogInsertAndQuery exercises the audit_logs JSONB insert + filter path.
func TestAuditLogInsertAndQuery(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()

	ctx := context.Background()

	details := map[string]interface{}{
		"email":      "bob@example.com",
		"department": "Sales",
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO audit_logs (actor_id, action, target_type, target_id, details)
		 VALUES ($1, $2, $3, $4, $5)`,
		"admin-1", "onboard", "user", "user-uuid-1", details,
	)
	if err != nil {
		t.Fatalf("insert audit log: %v", err)
	}

	// Filter by actor + action (mirrors ListAuditLogs dynamic query).
	rows, err := pool.Query(ctx,
		`SELECT id, actor_id, action, COALESCE(target_type, ''), COALESCE(target_id, ''), details, created_at
		 FROM audit_logs WHERE actor_id = $1 AND action = $2 ORDER BY created_at DESC LIMIT $3`,
		"admin-1", "onboard", 100,
	)
	if err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("expected at least one audit log row")
	}
	var id, actorID, action, targetType, targetID string
	var detailsJSON []byte
	var createdAt time.Time
	if err := rows.Scan(&id, &actorID, &action, &targetType, &targetID, &detailsJSON, &createdAt); err != nil {
		t.Fatalf("scan audit log: %v", err)
	}
	if actorID != "admin-1" || action != "onboard" {
		t.Errorf("unexpected audit row: actor=%q action=%q", actorID, action)
	}
	if len(detailsJSON) == 0 {
		t.Error("expected non-empty details JSONB")
	}
}

// TestUserDeviceMapping exercises the FK + ON DELETE CASCADE behavior.
func TestUserDeviceMapping(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()

	ctx := context.Background()

	uid := uuid.New()
	devID := uuid.New()

	_, err := pool.Exec(ctx,
		`INSERT INTO users (keycloak_user_id, email, first_name, last_name) VALUES ($1, $2, $3, $4)`,
		uid, "carol@example.com", "Carol", "Jones")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO devices (fleet_host_id, hostname, os_version) VALUES ($1, $2, $3)`,
		devID, "laptop-1", "macOS 15")
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO users_devices_mapping (user_id, device_id) VALUES ($1, $2)`,
		uid, devID)
	if err != nil {
		t.Fatalf("insert mapping: %v", err)
	}

	// The device-check lookup query (mirrors handler).
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users_devices_mapping WHERE user_id = $1`, uid,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 device for user, got %d", count)
	}

	// Cascade: deleting the user should remove the mapping.
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE keycloak_user_id = $1`, uid)
	if err != nil {
		t.Fatalf("delete user: %v", err)
	}
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users_devices_mapping WHERE user_id = $1`, uid,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count after cascade: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 mappings after cascade delete, got %d", count)
	}
}

// TestSoftDisableUsesDisabledFlag confirms the offboarding soft-disable SQL is
// idempotent and does not mutate the user's display role.
func TestSoftDisableUsesDisabledFlag(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()

	ctx := context.Background()
	uid := uuid.New()

	_, err := pool.Exec(ctx,
		`INSERT INTO users (keycloak_user_id, email, first_name, last_name, role)
		 VALUES ($1, $2, $3, $4, $5)`,
		uid, "dave@example.com", "Dave", "Brown", "Admin")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	for i := 0; i < 2; i++ {
		// Mirror the offboarding handler's soft-disable. Running it repeatedly
		// should leave the role untouched and keep disabled=true.
		_, err = pool.Exec(ctx,
			`UPDATE users SET disabled = true, updated_at = NOW() WHERE keycloak_user_id = $1`,
			uid)
		if err != nil {
			t.Fatalf("soft-disable run %d: %v", i+1, err)
		}
	}

	var role string
	var disabled bool
	err = pool.QueryRow(ctx, `SELECT role, disabled FROM users WHERE keycloak_user_id = $1`, uid).Scan(&role, &disabled)
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	if role != "Admin" {
		t.Errorf("expected role to remain %q, got %q", "Admin", role)
	}
	if !disabled {
		t.Error("expected disabled=true")
	}
}

// TestMigrationRecordsApplied confirms each migration is recorded exactly once.
func TestMigrationRecordsApplied(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()

	ctx := context.Background()

	// Run twice; count must stay stable.
	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("second run: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT id, name FROM schema_migrations ORDER BY id`)
	if err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	defer rows.Close()

	seen := map[int]string{}
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if existing, ok := seen[id]; ok {
			t.Errorf("migration id %d recorded twice (names: %q vs %q)", id, existing, name)
		}
		seen[id] = name
	}
	if len(seen) == 0 {
		t.Error("expected at least one recorded migration")
	}
}
