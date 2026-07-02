# FreeCloud Deployment & Operations

This is the operator runbook for a self-hosted production deployment using
`docker/docker-compose.prod.yml`.

## Prerequisites

- A host with Docker + the Compose plugin.
- **Three DNS records** pointing at the host (dashboard, API, Keycloak). Caddy
  obtains TLS certificates for each automatically, so they must resolve publicly.
- A real FleetDM instance (the production stack does **not** include the mock).

## Configure

```bash
cp .env.prod.example .env.prod
# Edit .env.prod — only the fields below are required.
```

### Required fields in .env.prod

| Variable | Purpose |
|---|---|
| `DASHBOARD_DOMAIN` / `API_DOMAIN` / `KC_DOMAIN` | Public hostnames Caddy serves + gets TLS for |
| `DASHBOARD_PUBLIC_URL` / `API_PUBLIC_URL` / `KC_PUBLIC_URL` | The `https://` URLs for the above |
| `FLEET_URL` / `FLEET_API_TOKEN` | Real FleetDM API endpoint + token |
| `POSTURE_CHECK_ENABLED` | Set to `"true"` to enforce device posture at login (optional) |

### Auto-generated secrets (do not set manually)

The following are generated automatically on first boot by the `secrets-init`
container and stored in `.secrets/secrets.env`. You do not set them in `.env.prod`:

`POSTGRES_PASSWORD`, `AUTH_SECRET`, `SCIM_BEARER_TOKEN`, `ACCESS_EVAL_TOKEN`,
`FLEET_WEBHOOK_SECRET`, `KC_ADMIN_PASSWORD`, `PROVISIONING_MASTER_KEY`

To rotate, delete `.secrets/secrets.env` and run `make prod-up` again. The
`KEYCLOAK_CLIENT_SECRET` is self-managed by the Epic A bootstrap and is not
stored in `.secrets/secrets.env`.

### Fleet, SMTP, and Identity Providers

These are configured **in-app** after first login — not via environment variables:

- **Settings → Fleet** — Fleet API URL and token (override the env-var values at runtime).
- **Settings → SMTP** — outbound email for notifications and invitations.
- **Settings → Identity Providers** — OIDC/SAML federation sources (Google Workspace,
  Azure AD, Okta, …).

## Deploy

```bash
make prod-up        # docker compose ... up -d --build
```

The backend self-bootstraps Keycloak on startup:

- Creates the `freecloud` realm, roles, and the `freecloud-service` confidential client.
- Registers the client secret it generated (no manual `make kc-setup` required).
- Runs database migrations.

There is **no** `setup_realm.sh` script to run. Do not run `make kc-setup` — it
has been removed.

### First login

Open `https://app.example.com`. You will be redirected to the setup wizard (`/setup`)
on a fresh database. Create your first admin account there. On subsequent boots the
wizard is skipped automatically (the realm is already provisioned).

### Verify

```bash
curl -fsS https://api.example.com/healthz     # liveness -> 200
curl -fsS https://api.example.com/readyz      # readiness (DB + Keycloak) -> 200
```

`/metrics` is exposed by the backend for Prometheus, but the bundled Caddyfile
returns 404 for public API traffic by default. Expose it only on an internal
network or behind authentication if you add scraping.

## Backup & Restore

Postgres holds the authoritative user↔Keycloak mappings, the audit log, **and**
Keycloak's own realm data, so back up the whole cluster.

Quick reference:

```bash
# Backup
DATABASE_URL=postgres://user:pass@host:5432/freecloud \
  scripts/backup.sh /var/backups/freecloud/

# Verify a backup (restore to scratch DB + assert row counts)
DATABASE_URL=postgres://... SCRATCH_DATABASE_URL=postgres://scratch.../postgres \
  scripts/verify-restore.sh
```

For the full runbook (restore steps, cron scheduling, retention policy) see
[docs/BACKUP_RESTORE.md](BACKUP_RESTORE.md). **Test a restore periodically** —
an untested backup is not a backup.

## Upgrades

1. Back up (above).
2. Pull/rebuild: `make prod-up` (rebuilds images and recreates containers).
3. The backend applies any new migrations on startup; watch logs for
   `database migrations completed`.
4. Smoke-test `/readyz` and a login.

**Rollback:** redeploy the previous image tag and, if a migration must be undone,
restore the pre-upgrade backup (migrations are forward-only).

## Troubleshooting

- **Backend exits immediately** with an `insecure configuration` error — a
  required secret is missing or an insecure default is set. The message lists
  every problem; fix `.env.prod`.
- **`/readyz` returns 503** — the body names the failing dependency (`database`
  or `keycloak`).
- **502 from offboard** — Keycloak could not disable the user (the account is not
  reliably locked); check Keycloak connectivity and retry.
- **Enrollment callback returns 401** — `FLEET_WEBHOOK_SECRET` mismatch between
  the backend and Fleet's webhook configuration.

## Scale note (v1)

This release targets a **single backend instance**. The rate limiter is
in-memory and migrations run on startup without a distributed lock; running
multiple backend replicas requires a shared (e.g. Redis) rate limiter and an
advisory-lock around migrations first. See
[docs/adr/0003-single-instance.md](adr/0003-single-instance.md) for the full
rationale.

## Observability

The backend exposes `/metrics` for Prometheus scraping. An optional Prometheus +
Grafana stack is available as a Compose overlay:

```bash
docker compose \
  -f docker/docker-compose.prod.yml \
  -f docker/docker-compose.observability.yml \
  --env-file .env.prod up -d
```

See [docs/OBSERVABILITY.md](OBSERVABILITY.md) for metrics reference, dashboard,
and alert rules.

## Outbound Provisioning

Per-app outbound provisioning lets FreeCloud push user create/update/deactivate
events to external systems when group membership or app assignment changes.

**Configure via the UI** at `/apps/{id}/provisioning`, or via the API:

```
PUT /api/v1/apps/{appId}/provisioning
```

**Supported connectors:**

| Connector | Status |
|---|---|
| Generic SCIM 2.0 | Fully functional — use this for real provisioning |
| Slack | Uses Slack SCIM with the saved bearer token |
| GitHub Org | Manages organization membership; use the organization field for the org name |

**Bearer token encryption:** connector tokens are encrypted at rest using
AES-256-GCM. Production installs generate `PROVISIONING_MASTER_KEY`
automatically into `.secrets/secrets.env`; custom deployments must provide a
base64-encoded 32-byte key:

```bash
openssl rand -base64 32   # generate the key
```

Add it to your secret store:

```
PROVISIONING_MASTER_KEY=<output of the command above>
```

In development / test mode only, a missing key falls back to base64 storage.
Production startup fails closed when the key is missing or malformed.

> **Note:** Slack and GitHub live sync still need tenant-level manual
> verification with real API credentials. The fast verification suite covers
> wiring and unit behavior only.

## Reports

The `/reports` endpoint generates a point-in-time compliance and user-lifecycle
report in CSV or JSON format.

```
GET /api/v1/reports          # download the current report (requires PermExportAuditLogs)
```

The report includes per-user onboard date, device compliance status, last MFA
event, and current app assignments. Use it for audit evidence or periodic
executive summaries.

## Conditional Access — Conditions

Per-app access policies support three condition types in addition to device
posture:

| Condition | Field | Example |
|---|---|---|
| Time of day | `allowed_hours` | `{"start": "08:00", "end": "18:00", "tz": "Europe/Berlin"}` |
| Network / IP | `allowed_networks` | `["10.0.0.0/8", "203.0.113.0/24"]` |
| Geography | `allowed_countries` | `["DE", "AT"]` |

Conditions are evaluated at login by the Keycloak authenticator SPI. All
specified conditions must pass for access to be allowed. Omit a field to skip
that condition.

**Preview / dry-run** a policy change before applying it:

```
POST /api/v1/apps/{appId}/policy/preview
```

The request body is the same JSON as `PUT .../policy`. The response returns
`{"allow": true/false, "reasons": [...]}` for a synthetic evaluation, without
modifying the stored policy.

### GeoIP (MaxMind GeoLite2)

The `Geography` condition above is **fail-closed by default**: FreeCloud ships
with a no-op GeoIP lookup that always reports "unknown", so any policy with a
non-empty `geoCountryAllowlist` denies every request until you supply a real
GeoIP database.

To enable live geo resolution:

1. **Get a GeoLite2 database.** Create a free MaxMind account and generate a
   license key at <https://www.maxmind.com/en/geolite2/signup>, then download
   `GeoLite2-Country.mmdb` (or `GeoLite2-City.mmdb` — both work; City is
   larger and includes extra fields FreeCloud doesn't use) via MaxMind's
   `geoipupdate` tool or the direct download link in your account console.
   FreeCloud does **not** auto-download this for you — MaxMind's license
   terms require you to accept them and use your own credentials.
2. **Place the file** on a volume the backend container can read, e.g.
   `/etc/freecloud/GeoLite2-Country.mmdb`.
3. **Set `GEOIP_MMDB_PATH`** to that path in `.env.prod` (or the container's
   environment) and mount the directory into the `backend` service in
   `docker-compose.prod.yml`.
4. Restart the backend. On startup it loads the file and wires it in; if the
   path is set but the file is missing, unreadable, or not a valid MaxMind DB,
   **the backend refuses to start** (fail closed) rather than boot with a geo
   gate that silently denies everyone.
5. **Keep it updated.** GeoLite2 databases are rebuilt roughly weekly; stale
   data degrades accuracy but never becomes unsafe (the lookup either
   resolves correctly or falls back to unknown/deny). Re-download
   periodically (MaxMind's `geoipupdate` can automate this) and restart the
   backend, or bind-mount a path that a host-side cron job refreshes in place.

If `GEOIP_MMDB_PATH` is unset, geography conditions keep failing closed and
every other condition type (time window, network/IP) is unaffected.

## Provisioning Dry-Run and Reconcile

**Dry-run** previews what a full provisioning push would do without sending any
requests to the downstream connector:

```
POST /api/v1/apps/{appId}/provisioning/dry-run
```

The response lists which users would be created, updated, or deactivated.
Run this after changing connector configuration to verify the expected delta
before committing.

**Reconcile-all** forces a re-sync of every user currently assigned to the app
against the downstream connector:

```
POST /api/v1/apps/{appId}/provisioning/reconcile-all
```

Use reconcile-all after a connector outage, after bulk-importing users, or when
the downstream system has drifted from FreeCloud's records. The operation is
idempotent — running it twice produces the same result.

## Recurring Access Reviews

Review schedules let you automate the creation of access-review campaigns on a
fixed cadence (e.g. quarterly).

**Create a schedule:**

```
POST /api/v1/review-schedules
{
  "name": "Quarterly app review",
  "cadence": "quarterly",
  "scope": "all-apps",
  "reviewer_role": "helpdesk"
}
```

**List / update / delete:**

```
GET    /api/v1/review-schedules
PATCH  /api/v1/review-schedules/{id}
DELETE /api/v1/review-schedules/{id}
```

All schedule endpoints require `PermManageCampaigns` (super-admin role).

**Export a completed campaign** (CSV / JSON):

```
GET /api/v1/campaigns/{id}/export
```

Requires `PermReviewCampaigns`. The export includes every item decision,
reviewer ID, and timestamp — suitable for audit evidence.

## Directory Federation (LDAP / Active Directory)

FreeCloud can federate with an existing LDAP directory or Active Directory,
importing users and groups via Keycloak's built-in LDAP provider.

**Configure via the UI** at Settings → Directory Federation, or via the API:

```
POST /api/v1/federation/sources
```

**Prerequisites:**

1. A running Keycloak instance (already required for the core stack).
2. `LDAP_BIND_PASSWORD` set in the backend's environment before creating any
   federation source — the backend passes this to Keycloak when it creates the
   federation component. Omitting it will cause the creation call to fail with
   `400 Bad Request`.

**How it works:** the backend stores the federation source config in its own DB
and creates a matching Keycloak LDAP federation component. Keycloak handles the
actual LDAP connection, credential binding, and attribute mapping.

**Trigger a sync** from the UI (Settings → Directory Federation → Sync Now), or
via the API:

```
POST /api/v1/federation/sources/{id}/sync
```

Sync is manual — there is no automatic periodic sync in v1.4. Schedule it
externally (e.g. a cron job calling the API endpoint with a valid API token) if
continuous sync is required.
