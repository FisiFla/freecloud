package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// migration is a single ordered, idempotent SQL migration.
type migration struct {
	id      int
	name    string
	statement string
}

// migrations is the ordered list of applied migrations. To add a new
// migration, append a new entry here with an incrementing id — the
// schema_migrations table ensures each runs exactly once.
var migrations = []migration{
	{
		id:        1,
		name:      "initial_schema",
		statement: Migration001,
	},
}

// Migration001 is the SQL for the initial schema migration, kept as a constant
// for backwards compatibility with any external callers that referenced it
// directly. New code should append to the migrations slice instead.
const Migration001 = `
CREATE TABLE IF NOT EXISTS users (
    keycloak_user_id UUID PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    first_name TEXT NOT NULL,
    last_name TEXT NOT NULL,
    department TEXT,
    role TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS devices (
    fleet_host_id UUID PRIMARY KEY,
    hostname TEXT,
    os_version TEXT,
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users_devices_mapping (
    user_id UUID REFERENCES users(keycloak_user_id) ON DELETE CASCADE,
    device_id UUID REFERENCES devices(fleet_host_id) ON DELETE CASCADE,
    assigned_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, device_id)
);

CREATE TABLE IF NOT EXISTS connected_apps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    keycloak_client_id TEXT UNIQUE,
    name TEXT NOT NULL,
    protocol TEXT CHECK (protocol IN ('OIDC', 'SAML')),
    base_url TEXT,
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS app_assignments (
    app_id UUID REFERENCES connected_apps(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(keycloak_user_id) ON DELETE CASCADE,
    assigned_at TIMESTAMPTZ DEFAULT NOW(),
    assigned_by TEXT,
    PRIMARY KEY (app_id, user_id)
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    details JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_devices_hostname ON devices(hostname);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor_action_created ON audit_logs(actor_id, action, created_at);
`

// RunMigrations applies any pending migrations in order, recording each in
// the schema_migrations table so it runs exactly once per database.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	logger := zap.L()

	// Ensure the migrations bookkeeping table exists.
	if _, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	// Determine which migrations have already been applied.
	rows, err := pool.Query(ctx, `SELECT id FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query applied migrations: %w", err)
	}
	applied := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan migration id: %w", err)
		}
		applied[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate applied migrations: %w", err)
	}
	rows.Close()

	pending := 0
	for _, m := range migrations {
		if applied[m.id] {
			continue
		}
		logger.Info("applying migration",
			zap.Int("id", m.id),
			zap.String("name", m.name),
		)
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", m.id, err)
		}
		if _, err := tx.Exec(ctx, m.statement); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration %d (%s) failed: %w", m.id, m.name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (id, name) VALUES ($1, $2)`,
			m.id, m.name,
		); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("record migration %d: %w", m.id, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.id, err)
		}
		pending++
	}

	logger.Info("database migrations completed",
		zap.Int("applied_now", pending),
		zap.Int("total", len(migrations)),
	)
	return nil
}
