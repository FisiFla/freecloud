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
| `POSTURE_CHECK_ENABLED` | Device-posture enforcement at login. **Defaults to `true`** in the production stack — a stack that forgets the variable must not silently run without posture checks. Set it to `false` only deliberately. |

### Auto-generated secrets (do not set manually)

The following are generated automatically on first boot by the `secrets-init`
container and stored in `.secrets/secrets.env`. You do not set them in `.env.prod`:

`POSTGRES_PASSWORD`, `AUTH_SECRET`, `SCIM_BEARER_TOKEN`, `ACCESS_EVAL_TOKEN`,
`FLEET_WEBHOOK_SECRET`, `KC_ADMIN_PASSWORD`, `PROVISIONING_MASTER_KEY`

The same values are also written one-per-file under `.secrets/files/`. The
production compose mounts them as Docker secrets and the services read them
through the `<VAR>_FILE` convention, so the values are **not** visible in
`docker inspect` or `/proc/<pid>/environ`. The backend additionally supports
`<VAR>_VAULT_PATH` (HashiCorp Vault) — see [docs/SECRETS.md](SECRETS.md).

`.secrets/` is created with mode `0700` and the secret files are owned by the
operator running `make prod-up` (the generator runs as your uid, not as root),
so `make prod-up` can read them on a Linux host. Run the documented
`make prod-up` / `make prod-secrets-init` entry points rather than invoking
`docker compose` directly, otherwise the files are created as root and the
next `make prod-up` fails with "Permission denied".

The `KEYCLOAK_CLIENT_SECRET` is self-managed by the Epic A bootstrap and is not
stored in `.secrets/secrets.env`.

### Rotating secrets

**Do not delete `.secrets/secrets.env`.** The generator only fills in the
*missing* keys and preserves every existing value, so deleting the file
regenerates the database and Keycloak passwords and breaks the running
cluster. Rotate one secret at a time instead:

| Secret | How to rotate | Effect |
|---|---|---|
| `AUTH_SECRET` | Remove the `AUTH_SECRET=` line from `.secrets/secrets.env`, run `make prod-secrets-init`, restart the frontend. | All dashboard sessions are invalidated. |
| `SCIM_BEARER_TOKEN` | Remove the line, re-run `make prod-secrets-init`, restart the backend. Update the SCIM client that calls you. | Inbound SCIM requests with the old token are rejected. |
| `ACCESS_EVAL_TOKEN` | Remove the line, re-run `make prod-secrets-init`, restart the backend **and** Keycloak. | The posture SPI can no longer evaluate access. |
| `FLEET_WEBHOOK_SECRET` | Remove the line, re-run `make prod-secrets-init`, restart the backend, and update the webhook secret in Fleet. | Enrollment callbacks with the old secret are rejected. Also rotates the device-cookie signing key unless `DEVICE_COOKIE_SECRET` is set. |
| `PROVISIONING_MASTER_KEY` | Remove the line, re-run `make prod-secrets-init`, restart the backend, and re-enter the connector tokens in Settings → Integrations. | Stored connector secrets have to be re-entered (they can no longer be decrypted). |
| `POSTGRES_PASSWORD` | **Not rotatable by re-running the generator** — the Postgres image applies the password only when the data directory is empty. Change it in the database and update the file:<br>`ALTER ROLE freecloud WITH PASSWORD '<new>';`<br>then set `POSTGRES_PASSWORD=<new>` in `.secrets/secrets.env`, re-run `make prod-secrets-init` and restart the backend. | The backend must be restarted with the new DSN. |
| `KC_ADMIN_PASSWORD` | **Not rotatable by re-running the generator** — Keycloak stores the admin password in its own database after first boot. Change it with `kcadm` (master realm) and update `.secrets/secrets.env` the same way as above. | The Keycloak master-realm admin password changes. |

After changing anything in `.secrets/secrets.env`, re-run
`make prod-secrets-init` so the per-key secret files are refreshed, then
recreate the affected services (`make prod-up`).

### Optional: `DEVICE_COOKIE_SECRET`

Set in `.secrets/secrets.env` (the generator preserves hand-added keys) to
HMAC-sign device-identity cookies independently of `FLEET_WEBHOOK_SECRET`.
Unset = fall back to the Fleet webhook secret (see `docs/SECRETS.md`).

### Fleet teams multi-tenant mapping (`fleet_team_orgs`)

New teams created via FreeCloud are recorded in `fleet_team_orgs` and
namespaced `{org_id}/{name}` in Fleet. Non–system-admins only list/mutate
mapped teams.

**Backfill unmapped legacy Fleet teams** into the Default Org (operator SQL,
run against FreeCloud Postgres after Migration046):

```sql
-- Example: map known Fleet team IDs into the default organization.
-- Replace (1),(2) with real Fleet team IDs from your Fleet UI/API.
INSERT INTO fleet_team_orgs (fleet_team_id, org_id, team_name)
VALUES
  (1, '00000000-0000-0000-0000-000000000001', 'legacy-team-1'),
  (2, '00000000-0000-0000-0000-000000000001', 'legacy-team-2')
ON CONFLICT (fleet_team_id) DO UPDATE
  SET org_id = EXCLUDED.org_id, team_name = EXCLUDED.team_name;
```

### Fleet, SMTP, and Identity Providers

These are configured **in-app** after first login — not via environment variables:

- **Settings → Fleet** — Fleet API URL and token (override the env-var values at runtime).
- **Settings → SMTP** — outbound email for notifications and invitations.
- **Settings → Identity Providers** — OIDC/SAML federation sources (Google Workspace,
  Azure AD, Okta, …).

The email channel has an env-var gate: the backend only wires the email
notifier when `NOTIFY_EMAIL=true` (default `false`). Set `NOTIFY_EMAIL=true` in
`.env.prod` to enable it. The SMTP settings saved in-app are the source of
truth; the `SMTP_*` variables in `.env.prod` are only a fallback for the case
where no SMTP configuration has been saved yet:

```bash
NOTIFY_EMAIL=true
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_FROM=freecloud@example.com
SMTP_TO=admin@example.com
SMTP_PASSWORD=CHANGE_ME
```

The same applies to the other channels: `NOTIFY_SLACK` + `SLACK_WEBHOOK_URL`
and `NOTIFY_WEBHOOK` + `WEBHOOK_URL` + `WEBHOOK_SECRET`.

## Deploy

```bash
make prod-up        # docker compose ... up -d --build
```

Startup sequence (all automatic, single command):

- A one-shot `migrate` container applies database migrations under an advisory
  lock, then exits; the backend only **verifies** the schema version at startup
  and refuses to serve if it is behind (`WAIT_FOR_SCHEMA_TIMEOUT` optionally
  makes it poll-wait instead).
- The backend self-bootstraps Keycloak: creates the `freecloud` realm, roles,
  and the `freecloud-service` confidential client, and registers the client
  secret it generated (no manual `make kc-setup` required).
- A dedicated `redis` container backs the shared rate limiter. `REDIS_URL` is
  **required in production** — the backend fails closed at startup without it
  (it is pre-wired in the prod compose file; nothing to configure).

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

## PostgreSQL TLS (sslmode)

The Postgres image in this stack generates a **self-signed** server certificate
on first boot (`docker/postgres/tls-entrypoint.sh`) and the production DSN
uses `sslmode=require`:

```
postgres://freecloud:<password>@postgres:5432/freecloud?sslmode=require
```

What that buys you, and what it does not:

- **Yes:** the connection is encrypted, so the password and the data are not
  readable on the wire.
- **No:** `sslmode=require` does **not** verify the server certificate. Any
  container that can reach `postgres` on the Compose network can present its
  own certificate and man-in-the-middle the connection. The encryption is only
  meaningful against an attacker who cannot reach the internal network.

This is a deliberate trade-off for a single-host deployment where the database
is only reachable from the other containers on the Compose bridge network. If
your threat model includes a compromised container on that network, strengthen
it to `verify-full` with a CA you control:

1. Create a CA and a server certificate for the `postgres` hostname
   (`openssl` with a `subjectAltName` of `postgres`), and store the CA
   certificate on the host.
2. Mount the CA certificate into the containers that connect to the database
   (`backend` and `migrate`), e.g. add to the `volumes:` list of both services:
   `./postgres/ca.crt:/etc/freecloud/postgres-ca.crt:ro`.
3. Point the DSN at the CA and require full verification:
   `?sslmode=verify-full&sslrootcert=/etc/freecloud/postgres-ca.crt`.
4. Configure Postgres to use the CA-signed certificate (replace the
   self-signed one in `docker/postgres/tls-entrypoint.sh`).

Note that the backend image is built from `backend/Dockerfile` and the
`sslrootcert` path has to exist inside it, so the certificate has to be
mounted (as above) rather than baked into the image.

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
3. The one-shot `migrate` container applies any new migrations before the
   backend starts; check `docker compose logs migrate` if the backend reports
   the schema is behind.
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

## Scale note

As of v1.7 the backend is **multi-instance capable**: rate limiting is shared
via Redis, migrations run in a dedicated one-shot job under an advisory lock,
Keycloak bootstrap is advisory-lock-serialised, and background jobs (reconcile,
audit retention, snapshots) use Postgres advisory-lock leader election so
exactly one replica runs each. This is proven in CI by a two-replica e2e suite
behind a load balancer. Replicas must share the same Postgres and Redis. See
[docs/adr/0004-multi-instance-ha.md](adr/0004-multi-instance-ha.md) (supersedes
ADR 0003).

## Observability

The backend exposes `/metrics` for Prometheus scraping. An optional Prometheus +
Grafana stack is available as a Compose overlay:

```bash
# The secrets have to be in the shell environment — the production compose
# resolves the database DSN at parse time and fails fast without it.
set -a && . .secrets/secrets.env && set +a
docker compose \
  -f docker/docker-compose.prod.yml \
  -f docker/docker-compose.observability.yml \
  --env-file .env.prod up -d
```

See [docs/OBSERVABILITY.md](OBSERVABILITY.md) for metrics reference, dashboard,
and alert rules.

## Pinned image digests

Every base image in `docker/` is pinned to a digest, not just a tag:

```yaml
image: postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea  # postgres 16-alpine
```

The comment after the digest records the tag the digest belongs to, so the
line stays readable. The same convention is used in the Dockerfiles
(`FROM postgres:16-alpine@sha256:…`).

**Refreshing a digest** (after a version bump, or to pick up a rebuilt tag):

```bash
# Docker Hub
docker buildx imagetools inspect postgres:16-alpine --format '{{.Manifest.Digest}}'

# quay.io / gcr.io
docker buildx imagetools inspect quay.io/keycloak/keycloak:25.0 --format '{{.Manifest.Digest}}'
```

Or, without Docker:

```bash
TOKEN=$(curl -s "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/postgres:pull" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['token'])")
curl -sI -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.docker.distribution.manifest.list.v2+json" \
  https://registry-1.docker.io/v2/library/postgres/manifests/16-alpine \
  | grep -i docker-content-digest
```

Replace the digest in the compose file / Dockerfile, keep the tag comment in
sync, and re-run `make prod-up`.

The images in `backend/Dockerfile` and `frontend/Dockerfile` are pinned the
same way.

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

### Connector Verification

Every connector's exact request/response contract (methods, paths, headers,
bodies — including the offboard-deactivation path) is pinned by recorded-
fixture tests that run in the normal test suite:
`backend/internal/provisioning/{github,slack}_connector_contract_test.go`.
These assert against real GitHub REST API and Slack SCIM API shapes without
requiring any live credentials, and run in CI on every change.

**GitHub Org** is FreeCloud's own naming — it manages GitHub Organization
*membership* via GitHub's REST API
(`PUT`/`DELETE /orgs/{org}/memberships|members/{username}`), not GitHub's
separate Enterprise SCIM v2 API. This is intentional (org membership covers
the common "grant/revoke access to the org" use case without requiring a
GitHub Enterprise plan) — see the contract test file's header for details if
you're expecting SCIM semantics here.

**Optional live-tenant verification.** The recorded-fixture tests above prove
the connector code is correct against the documented API contract, but
they've never run against a real GitHub org or Slack workspace. To do that:

```bash
# GitHub — requires a PAT/App token with org:write + admin:org, and an
# existing GitHub account you control in the target org (membership invites
# an existing user; it can't create a GitHub account).
GITHUB_SCIM_TOKEN=ghp_xxx \
GITHUB_SCIM_ORG=my-test-org \
GITHUB_SCIM_TEST_USERNAME=my-disposable-test-account \
make verify-provisioning-live
```

Each target is skipped entirely (exit 0) when its env vars are absent, so
`make verify-provisioning-live` is safe to leave in CI with no credentials
configured — it just reports "SKIPPED" for both. `GITHUB_SCIM_BASE_URL`
overrides the API root for GitHub Enterprise Server (on-prem).

**Slack live verification stays parked.** It requires a paid Slack plan with
SCIM provisioning enabled — see
[Slack's SCIM API docs](https://docs.slack.dev/admins/scim-api/). If/when a
suitable workspace is available, set `SLACK_SCIM_TOKEN` +
`SLACK_SCIM_TEST_EMAIL` and the same `make verify-provisioning-live` target
exercises create → update → deactivate against it and cleans up.

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

### Reverse Proxy Requirements (SPI Client-IP Forwarding)

The Keycloak authenticator SPI (`keycloak-authenticator/`) forwards the
resolved client IP to `access/evaluate` for the Network and Geography
conditions above. By default it uses the direct TCP peer address of whoever
connects to Keycloak. If Keycloak sits behind a reverse proxy or load
balancer (the normal production topology — Caddy, nginx, an ALB, etc.), that
peer address is always the proxy's own address, not the real end-user IP, so
network/geo conditions would evaluate against the wrong IP.

To fix this, set `TRUST_PROXY=true` on the Keycloak container/pod. When
enabled, the SPI reads `X-Forwarded-For` and uses the **rightmost** entry —
not the leftmost. Reverse proxies conventionally *append* the connecting
peer's address to any existing `X-Forwarded-For` value rather than replacing
it (nginx's `$proxy_add_x_forwarded_for`, Caddy's default `reverse_proxy`
behavior, and equivalents on managed load balancers all do this). That means:

```
X-Forwarded-For: <anything-the-client-sent>, <your-proxy's-real-peer-address>
```

Only the rightmost hop is guaranteed to have been set by infrastructure you
control — everything to its left is attacker-controlled input carried
through unmodified. Taking the leftmost entry (a common mistake) lets anyone
who can reach the proxy directly forge an arbitrary "client IP" and bypass
network/geo conditions.

**This only works if there is exactly one reverse-proxy hop and it is the
only path to Keycloak.** `TRUST_PROXY=true` is a blanket setting — it doesn't
distinguish "this request came through my proxy" from "this request hit
Keycloak directly." If Keycloak's port is *also* reachable directly (bypassing
the proxy) while `TRUST_PROXY=true` is set, an attacker with direct access can
send a single forged `X-Forwarded-For` value that becomes the (only, hence
rightmost) hop and is trusted outright. **You must firewall/network-isolate
Keycloak so the reverse proxy is the only thing that can reach it** — never
expose Keycloak's port directly to the same network the proxy is meant to
gate access from.

Configuration checklist:

1. Deploy exactly one reverse proxy in front of Keycloak.
2. Ensure Keycloak is not reachable on any network path that bypasses that proxy.
3. Set `TRUST_PROXY=true` in the Keycloak container's environment.
4. Confirm your proxy is configured to forward (append to) `X-Forwarded-For`
   — this is the default for Caddy's `reverse_proxy` and nginx's
   `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for`.

If `TRUST_PROXY` is unset or `false` (the default), `X-Forwarded-For` is
never read at all, and network/geo conditions evaluate the direct connection
IP — safe by default, but requires no proxy in front of Keycloak for those
conditions to see the real client IP.

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
