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
# Fill in domains and secrets. Generate secrets with: openssl rand -base64 33
```

### Environment reference

| Variable | Purpose |
|---|---|
| `DASHBOARD_DOMAIN` / `API_DOMAIN` / `KC_DOMAIN` | Public hostnames Caddy serves + gets TLS for |
| `DASHBOARD_PUBLIC_URL` / `API_PUBLIC_URL` / `KC_PUBLIC_URL` | The `https://` URLs for the above |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | Database credentials |
| `KEYCLOAK_ADMIN` / `KEYCLOAK_ADMIN_PASSWORD` | Keycloak bootstrap admin |
| `KEYCLOAK_REALM` | Realm name (default `freecloud`) |
| `KEYCLOAK_CLIENT_ID` / `KEYCLOAK_CLIENT_SECRET` | Backend service-account (confidential) client |
| `KEYCLOAK_AUDIENCE` | Expected JWT audience (default `freecloud-dashboard`) |
| `FLEET_URL` / `FLEET_API_TOKEN` | Real FleetDM API endpoint + token |
| `FLEET_WEBHOOK_SECRET` | HMAC key for the Fleet enrollment callback |
| `SCIM_BEARER_TOKEN` | Dedicated bearer token for inbound SCIM provisioning |
| `ACCESS_EVAL_TOKEN` | Dedicated bearer token for posture access-evaluation calls |
| `AUTH_SECRET` | Auth.js session-signing secret (frontend) |
| `AUTH_KEYCLOAK_ID` / `AUTH_KEYCLOAK_SECRET` | Frontend OIDC client |

## Deploy

```bash
make prod-up        # docker compose ... up -d --build
```

After the first boot, create the realm, groups, and the `freecloud-service`
confidential client (run against the Keycloak instance):

```bash
APP_ENV=production ALLOW_DEV_SETUP=true CREATE_DEMO_USER=false \
  KEYCLOAK_URL=https://auth.example.com KEYCLOAK_CLIENT_SECRET=... make kc-setup
```

The backend runs database migrations automatically on startup.

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
| Slack | Config + token stored; live sync not yet implemented |
| GitHub Org | Config + token stored; live sync not yet implemented |

**Bearer token encryption:** connector tokens are encrypted at rest using
AES-256-GCM. Set `PROVISIONING_MASTER_KEY` to a base64-encoded 32-byte key:

```bash
openssl rand -base64 32   # generate the key
```

Add it to `.env.prod`:

```
PROVISIONING_MASTER_KEY=<output of the command above>
```

In development / test mode (key absent) tokens are stored base64-only without
encryption. The backend will log a warning at startup if the key is missing and
`APP_ENV=production`.

> **Note:** Slack and GitHub connectors store configuration but do not perform
> live sync. Use the Generic SCIM 2.0 connector for production outbound
> provisioning until those connectors are fully implemented.

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
