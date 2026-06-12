package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Migration001 is the SQL schema migration for the FreeCloud database.
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

// RunMigrations executes the database schema migration.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	logger := zap.L()
	logger.Info("running database migrations...")

	_, err := pool.Exec(ctx, Migration001)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	logger.Info("database migrations completed successfully")
	return nil
}
