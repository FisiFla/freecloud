package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// migration is a single ordered, idempotent SQL migration.
type migration struct {
	id        int
	name      string
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
	{
		id:        2,
		name:      "user_disabled_flag",
		statement: Migration002,
	},
	{
		id:        3,
		name:      "device_text_ids_and_enrollment_tokens",
		statement: Migration003,
	},
	{
		id:        4,
		name:      "scim_resource_versions",
		statement: Migration004,
	},
	{
		id:        5,
		name:      "app_access_policies",
		statement: Migration005,
	},
	{
		id:        15,
		name:      "api_tokens",
		statement: Migration015,
	},
	{
		id:        16,
		name:      "review_campaigns",
		statement: Migration016,
	},
	{
		id:        17,
		name:      "access_requests",
		statement: Migration017,
	},
	{
		id:        20,
		name:      "analytics_snapshots",
		statement: Migration020,
	},
	{
		id:        21,
		name:      "siem_cursor",
		statement: Migration021,
	},
	{
		id:        22,
		name:      "audit_logs_seq",
		statement: Migration022,
	},
	{
		id:        23,
		name:      "enrollment_tokens_used_by_host_id",
		statement: Migration023,
	},
	{
		id:        28,
		name:      "audit_logs_hash_chain",
		statement: Migration028,
	},
	{
		id:        29,
		name:      "approval_requests",
		statement: Migration029,
	},
	{
		id:        30,
		name:      "approval_requests_payload",
		statement: Migration030,
	},
	{
		id:        31,
		name:      "approval_requests_indexes",
		statement: Migration031,
	},
	{
		id:        32,
		name:      "approval_requests_executing_status",
		statement: Migration032,
	},
	{
		id:        33,
		name:      "audit_chain_anchors",
		statement: Migration033,
	},
	{
		id:        34,
		name:      "device_commands",
		statement: Migration034,
	},
	{
		id:        35,
		name:      "mfa_self_service",
		statement: Migration035,
	},
	{
		id:        36,
		name:      "provisioning_state_and_config",
		statement: Migration036,
	},
	{
		id:        37,
		name:      "federation_sources",
		statement: Migration037,
	},
	{
		id:        38,
		name:      "device_posture_cache",
		statement: Migration038,
	},
	{
		id:        39,
		name:      "access_policy_conditions",
		statement: Migration039,
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

// Migration002 adds an explicit disabled state for users. Older local data may
// have encoded disabled state by appending " (DISABLED)" to role, so this
// migration backfills the flag and cleans the display role.
const Migration002 = `
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled BOOLEAN NOT NULL DEFAULT false;

UPDATE users
SET disabled = true,
    role = NULLIF(TRIM(REGEXP_REPLACE(COALESCE(role, ''), '([[:space:]]*\(DISABLED\))+[[:space:]]*$', '')), '')
WHERE COALESCE(role, '') LIKE '%(DISABLED)%';
`

// Migration003 widens Fleet host identifiers from UUID to TEXT (real FleetDM
// host IDs are opaque strings/numeric, not UUIDs) and adds the enrollment_tokens
// table that links a Fleet enrollment token to the user it was issued for, so a
// device that later enrolls can be mapped to its owner.
const Migration003 = `
ALTER TABLE users_devices_mapping DROP CONSTRAINT IF EXISTS users_devices_mapping_device_id_fkey;
ALTER TABLE devices ALTER COLUMN fleet_host_id TYPE TEXT USING fleet_host_id::text;
ALTER TABLE users_devices_mapping ALTER COLUMN device_id TYPE TEXT USING device_id::text;
ALTER TABLE users_devices_mapping
    ADD CONSTRAINT users_devices_mapping_device_id_fkey
    FOREIGN KEY (device_id) REFERENCES devices(fleet_host_id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_devices_fleet_host_id ON devices(fleet_host_id);

CREATE TABLE IF NOT EXISTS enrollment_tokens (
    token       TEXT PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(keycloak_user_id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    used_at     TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_enrollment_tokens_user_id ON enrollment_tokens(user_id);
`

// Migration004 adds scim_resource_versions to track SCIM ETag/meta.version
// and meta.lastModified per resource (keyed by keycloak_user_id). SCIM requires
// version tracking for optimistic concurrency (If-Match / ETag); storing it
// in Postgres keeps it durable and prevents version skew on restarts.
const Migration004 = `
CREATE TABLE IF NOT EXISTS scim_resource_versions (
    user_id    UUID PRIMARY KEY REFERENCES users(keycloak_user_id) ON DELETE CASCADE,
    version    BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scim_resource_versions_updated_at ON scim_resource_versions(updated_at);
`

// Migration005 adds per-app posture requirements for the conditional access
// feature (A3). Each connected app can independently require that the
// authenticating device be enrolled, disk-encrypted, free of critical
// vulnerabilities, and/or running a sufficiently recent OS.
const Migration005 = `
CREATE TABLE IF NOT EXISTS app_access_policies (
    app_id                  UUID PRIMARY KEY REFERENCES connected_apps(id) ON DELETE CASCADE,
    require_enrolled        BOOLEAN NOT NULL DEFAULT false,
    require_disk_encrypted  BOOLEAN NOT NULL DEFAULT false,
    require_no_critical_vulns BOOLEAN NOT NULL DEFAULT false,
    max_os_age_days         INTEGER,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// Migration015 creates the api_tokens table for C2 service-account API tokens.
// Only the SHA-256 hash of the token is stored; the plaintext is shown once at
// creation time and never persisted.
const Migration015 = `
CREATE TABLE IF NOT EXISTS api_tokens (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL,
    token_hash       TEXT UNIQUE NOT NULL,
    role             TEXT NOT NULL,
    scopes           TEXT[] NOT NULL DEFAULT '{}',
    service_identity TEXT NOT NULL,
    created_by       TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at       TIMESTAMPTZ,
    revoked_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_token_hash ON api_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_api_tokens_service_identity ON api_tokens(service_identity);
`

// Migration016 creates tables for C3 access review campaigns.
const Migration016 = `
CREATE TABLE IF NOT EXISTS review_campaigns (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'completed', 'cancelled')),
    created_by  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS review_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id   UUID NOT NULL REFERENCES review_campaigns(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(keycloak_user_id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL CHECK (resource_type IN ('app', 'group')),
    resource_id   TEXT NOT NULL,
    resource_name TEXT NOT NULL,
    decision      TEXT CHECK (decision IN ('confirm', 'revoke')),
    decided_by    TEXT,
    decided_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_review_items_campaign_id ON review_items(campaign_id);
CREATE INDEX IF NOT EXISTS idx_review_items_user_id ON review_items(user_id);
`

// Migration017 adds access_requests for C4 self-service access requests.
const Migration017 = `
CREATE TABLE IF NOT EXISTS access_requests (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    requester_id UUID NOT NULL REFERENCES users(keycloak_user_id) ON DELETE CASCADE,
    app_id       UUID NOT NULL REFERENCES connected_apps(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'approved', 'rejected')),
    reason       TEXT,
    decided_by   TEXT,
    decided_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (requester_id, app_id, status)
);

CREATE INDEX IF NOT EXISTS idx_access_requests_requester ON access_requests(requester_id);
CREATE INDEX IF NOT EXISTS idx_access_requests_status ON access_requests(status);
`

// Migration020 creates the analytics_snapshots time-series table (D2 / FCEX2-18).
const Migration020 = `
CREATE TABLE IF NOT EXISTS analytics_snapshots (
    id               BIGSERIAL PRIMARY KEY,
    captured_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    compliance_rate  DOUBLE PRECISION NOT NULL DEFAULT 0,
    enrolled_devices INTEGER NOT NULL DEFAULT 0,
    mfa_coverage_pct DOUBLE PRECISION NOT NULL DEFAULT 0,
    app_count        INTEGER NOT NULL DEFAULT 0,
    onboard_count    INTEGER NOT NULL DEFAULT 0,
    offboard_count   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_analytics_snapshots_captured_at ON analytics_snapshots(captured_at DESC);
`

// Migration021 creates the SIEM streaming cursor table (D3 / FCEX2-19).
// A single row (id=1) holds the monotonic seq of the last audit_log entry
// successfully delivered to the external sink.
const Migration021 = `
CREATE TABLE IF NOT EXISTS siem_cursor (
    id       INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_seq BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO siem_cursor (id, last_seq) VALUES (1, 0) ON CONFLICT DO NOTHING;
`

// Migration022 adds a BIGSERIAL seq column to audit_logs used as a durable
// monotonic cursor by the SIEM streamer (D3 / FCEX2-19). Existing rows receive
// seq values via the DEFAULT sequence; new inserts get auto-assigned values.
const Migration022 = `
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS seq BIGSERIAL;
CREATE INDEX IF NOT EXISTS idx_audit_logs_seq ON audit_logs(seq);
`

// Migration023 adds a used_by_host_id column to enrollment_tokens so the
// FleetDM enrollment callback can record which device consumed a given token.
// This enables the device-identity cookie endpoint (A3 / FCEX3-7) to look up
// the enrolled fleet_host_id from an enrollment token without re-querying
// users_devices_mapping — the token row itself becomes the link.
//
// Plain TEXT (no FK): the enrollment callback sets used_by_host_id in the same
// transaction that upserts the devices row, and the token UPDATE necessarily
// runs before the device INSERT (it gates token consumption), so a FK to
// devices(fleet_host_id) would be violated mid-transaction. The column is only
// a lookup hint, so referential integrity is unnecessary.
const Migration023 = `
ALTER TABLE enrollment_tokens ADD COLUMN IF NOT EXISTS used_by_host_id TEXT;
`

// Migration028 adds row_hash + prev_hash to audit_logs for tamper-evident
// hash-chaining (C1 / FCEX3-13). Existing rows are backfilled in seq order:
// each row's hash is computed over its canonical fields plus the previous row's
// hash. Rows written before this migration have empty details treated as '{}';
// the UPDATE uses a window-function CTE to walk the chain in one pass via a
// recursive approach implemented as a PL/pgSQL DO block for portability.
// New inserts use the WriteEntry helper (internal/audit/chain.go) which
// computes hashes before the INSERT, keeping the DB free of trigger logic.
const Migration028 = `
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS row_hash TEXT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS prev_hash TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_logs_row_hash ON audit_logs(row_hash);

-- Backfill existing rows in seq order using a PL/pgSQL loop.
DO $$
DECLARE
    r   RECORD;
    ph  TEXT := '';
    h   TEXT;
BEGIN
    FOR r IN
        SELECT seq, actor_id, action,
               COALESCE(target_type, '') AS target_type,
               COALESCE(target_id, '')   AS target_id,
               COALESCE(details::text, '{}') AS details
        FROM audit_logs
        WHERE row_hash IS NULL
        ORDER BY seq ASC
    LOOP
        -- Mirror Go's computeHash: sha256( len:field| for each of 6 fields ).
        -- We replicate the canonical format in SQL for the backfill only;
        -- new rows are hashed in Go before INSERT.
        h := encode(
            sha256(
                convert_to(
                    octet_length(r.actor_id)::text    || ':' || r.actor_id    || '|' ||
                    octet_length(r.action)::text      || ':' || r.action      || '|' ||
                    octet_length(r.target_type)::text || ':' || r.target_type || '|' ||
                    octet_length(r.target_id)::text   || ':' || r.target_id   || '|' ||
                    octet_length(r.details)::text     || ':' || r.details     || '|' ||
                    octet_length(ph)::text            || ':' || ph            || '|',
                    'UTF8'
                )
            ),
            'hex'
        );
        UPDATE audit_logs SET row_hash = h, prev_hash = ph WHERE seq = r.seq;
        ph := h;
    END LOOP;
END;
$$;
`

// Migration029 creates the approval_requests table (C4 / FCEX3-16).
// A privileged action (onboard / offboard) submitted by helpdesk is stored
// here pending super-admin review; the actual KC/Fleet action runs only on
// approval.
const Migration029 = `
CREATE TABLE IF NOT EXISTS approval_requests (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action_type  TEXT NOT NULL CHECK (action_type IN ('onboard', 'offboard')),
    requester_id TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'approved', 'rejected')),
    decided_by   TEXT,
    decided_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// Migration030 adds the payload column to approval_requests.
// Stored as JSONB so both onboard (user details) and offboard (user ID) fit
// in a single table without per-action-type columns.
const Migration030 = `
ALTER TABLE approval_requests ADD COLUMN IF NOT EXISTS payload JSONB NOT NULL DEFAULT '{}';
`

// Migration031 adds indexes on approval_requests used by the list endpoints.
const Migration031 = `
CREATE INDEX IF NOT EXISTS idx_approval_requests_status     ON approval_requests(status);
CREATE INDEX IF NOT EXISTS idx_approval_requests_requester  ON approval_requests(requester_id);
CREATE INDEX IF NOT EXISTS idx_approval_requests_created_at ON approval_requests(created_at DESC);
`

// Migration032 allows an approval request to be claimed while its approved
// action is executing, without exposing it as approved before the action
// actually succeeds.
const Migration032 = `
ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_status_check;
ALTER TABLE approval_requests
    ADD CONSTRAINT approval_requests_status_check
    CHECK (status IN ('pending', 'executing', 'approved', 'rejected'));
`

// Migration033 stores the retained-chain anchor used after audit retention
// pruning. When old audit rows are deleted from the start of the chain,
// VerifyChain starts from this recorded predecessor hash instead of requiring
// the first surviving row to look like a genesis row.
const Migration033 = `
CREATE TABLE IF NOT EXISTS audit_chain_anchors (
    id            INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    first_seq     BIGINT NOT NULL,
    prev_hash     TEXT NOT NULL,
    pruned_before TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// Migration034 adds the device_commands table for E2 (command status/history).
// Records every MDM command issued via the FreeCloud API so admins can audit
// what was sent to each device and whether Fleet acknowledged it.
const Migration034 = `
CREATE TABLE IF NOT EXISTS device_commands (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host_id           TEXT NOT NULL,
    command_type      TEXT NOT NULL CHECK (command_type IN ('lock','lock_message','restart','clear_passcode','wipe')),
    status            TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','done','failed')),
    requested_by      TEXT NOT NULL,
    requested_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    fleet_command_uuid TEXT,
    result            TEXT
);
CREATE INDEX IF NOT EXISTS idx_device_commands_host_id      ON device_commands(host_id);
CREATE INDEX IF NOT EXISTS idx_device_commands_requested_at ON device_commands(requested_at DESC);
`

// Migration035 adds the tables required for MFA self-service (B1):
//
//   - mfa_recovery_codes: hashed single-use backup codes for MFA users.
//   - mfa_coverage_cache: a per-user cache of MFA enrollment state used by the
//     analytics snapshot job to compute mfa_coverage_pct without a live
//     Keycloak round-trip.
const Migration035 = `
CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    TEXT        NOT NULL,
    code_hash  TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    used_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_mfa_recovery_codes_user ON mfa_recovery_codes(user_id);

CREATE TABLE IF NOT EXISTS mfa_coverage_cache (
    user_id    TEXT        PRIMARY KEY,
    has_mfa    BOOLEAN     NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// Migration036 creates the provisioning_state and app_provisioning_config tables
// for Epic A outbound provisioning (A1).
const Migration036 = `
CREATE TABLE IF NOT EXISTS provisioning_state (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES connected_apps(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(keycloak_user_id) ON DELETE CASCADE,
    remote_id       TEXT,
    status          TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'provisioned', 'deprovisioned', 'error', 'permanent_error')),
    last_sync_at    TIMESTAMPTZ,
    last_error      TEXT,
    retry_count     INTEGER NOT NULL DEFAULT 0,
    next_retry_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, user_id)
);

CREATE TABLE IF NOT EXISTS app_provisioning_config (
    app_id              UUID PRIMARY KEY REFERENCES connected_apps(id) ON DELETE CASCADE,
    enabled             BOOLEAN NOT NULL DEFAULT false,
    connector_type      TEXT NOT NULL DEFAULT 'scim',
    endpoint_url        TEXT,
    bearer_token_hash   TEXT,
    bearer_token_enc    TEXT,
    attribute_map       JSONB NOT NULL DEFAULT '{}',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_provisioning_state_app_user ON provisioning_state(app_id, user_id);
CREATE INDEX IF NOT EXISTS idx_provisioning_state_status ON provisioning_state(status);
CREATE INDEX IF NOT EXISTS idx_provisioning_state_next_retry ON provisioning_state(next_retry_at) WHERE next_retry_at IS NOT NULL;
`

// Migration037 creates the federation_sources table for LDAP/AD directory
// federation (C1). Each row represents one configured LDAP/AD source with its
// Keycloak user-storage component ID and last-sync metadata.
const Migration037 = `
CREATE TABLE IF NOT EXISTS federation_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    provider_type TEXT NOT NULL DEFAULT 'ldap',
    vendor TEXT NOT NULL DEFAULT 'other',
    config JSONB NOT NULL DEFAULT '{}',
    keycloak_component_id TEXT UNIQUE,
    last_sync_at TIMESTAMPTZ,
    last_sync_status TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_federation_sources_provider_type ON federation_sources(provider_type);
`

// Migration038 creates the device_posture_cache table for A1 (real
// compliance_rate). Each row records the last-known compliance posture for one
// Fleet host so the analytics snapshot job can compute compliance_rate from DB
// without a live Fleet round-trip on every snapshot tick.
const Migration038 = `
CREATE TABLE IF NOT EXISTS device_posture_cache (
    host_id          TEXT        PRIMARY KEY,
    compliant        BOOLEAN     NOT NULL DEFAULT FALSE,
    disk_encrypted   BOOLEAN     NOT NULL DEFAULT FALSE,
    os_up_to_date    BOOLEAN     NOT NULL DEFAULT TRUE,
    needs_update     BOOLEAN     NOT NULL DEFAULT FALSE,
    firewall_enabled BOOLEAN     NOT NULL DEFAULT FALSE,
    checked_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_device_posture_cache_checked_at ON device_posture_cache(checked_at DESC);
`

// Migration039 adds conditional-access policy columns to app_access_policies
// (D1). The new columns extend per-app policies with time-window, network
// allowlist, and geo-country allowlist conditions evaluated at access time.
const Migration039 = `
ALTER TABLE app_access_policies
    ADD COLUMN IF NOT EXISTS allowed_time_start TIME,
    ADD COLUMN IF NOT EXISTS allowed_time_end   TIME,
    ADD COLUMN IF NOT EXISTS network_allowlist     TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS geo_country_allowlist TEXT[] NOT NULL DEFAULT '{}';
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
