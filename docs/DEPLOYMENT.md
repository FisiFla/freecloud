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

```bash
# Backup (whole cluster: freecloud + keycloak DBs)
docker compose -f docker/docker-compose.prod.yml --env-file .env.prod \
  exec -T postgres pg_dumpall -U "$POSTGRES_USER" > freecloud-$(date +%F).sql

# Restore into a fresh postgres volume
cat freecloud-YYYY-MM-DD.sql | docker compose -f docker/docker-compose.prod.yml \
  --env-file .env.prod exec -T postgres psql -U "$POSTGRES_USER" -d postgres
```

Schedule the backup from host cron (daily) and copy the dump off-host. **Test a
restore periodically** — an untested backup is not a backup.

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
advisory-lock around migrations first.
