# Quickstart

Get FreeCloud running locally in under 5 minutes.

## Prerequisites

- Docker 24+ with the Compose plugin (`docker compose version`)
- Go 1.25+
- Node.js 26+
- `make`

## 1. Clone and configure

```bash
git clone https://github.com/FisiFla/freecloud.git
cd freecloud
```

No extra config step is needed for local development — the backend picks up
safe dev defaults automatically when `APP_ENV=development`.

## 2. Start infrastructure

```bash
make dev-up
```

This starts three containers and waits for them to be healthy:

| Service | Container | Port |
|---|---|---|
| PostgreSQL 16 | `postgres` | 5432 |
| Keycloak 25 | `keycloak` | 8081 |
| FleetDM mock | `fleetdm-mock` | 8082 |

Postgres is initialized with both a `freecloud` database (for the backend) and a
`keycloak` database (for Keycloak) via `docker/postgres/init-keycloak-db.sql`.

`make dev-up` also runs `make db-migrate` automatically. If you need to run
migrations separately:

```bash
make db-migrate
```

## 3. Configure Keycloak

```bash
make kc-setup
```

This runs `backend/cmd/scripts/setup_realm.sh`, which creates:
- The `freecloud` realm.
- Standard groups (`admins`, `users`, etc.).
- The `freecloud-service` confidential client (least-privilege: `manage-users` +
  `manage-clients`).
- The `freecloud-dashboard` client for the frontend.
- A demo admin user (`admin` / `admin`).

> Only needed once per fresh infrastructure start. Re-running is idempotent.

## 4. Start the backend

```bash
cd backend && APP_ENV=development go run cmd/server/main.go
```

`APP_ENV=development` opts into the dev defaults (insecure local credentials,
no TLS requirement). Without it the backend fails closed and refuses to start
on dev defaults.

The server listens on `:8080` and prints a startup log line when ready.

## 5. Start the frontend

```bash
cd frontend && npm install && npm run dev
```

The dev server listens on `:3000` and proxies API requests to `http://localhost:8080`.

## 6. Sign in

Open [http://localhost:3000](http://localhost:3000).

Sign in with the demo admin account created by `make kc-setup`:

| Field | Value |
|---|---|
| Username | `admin` |
| Password | `admin` |

## Ports at a glance

| Service | Port |
|---|---|
| Frontend (Next.js dev) | 3000 |
| Backend API | 8080 |
| Keycloak | 8081 |
| FleetDM mock | 8082 |
| PostgreSQL | 5432 |

## Environment variables (development)

The backend reads these from the environment; the dev defaults are listed in
`backend/internal/config/config.go`. The only variable you need to set locally is:

```bash
APP_ENV=development   # required — disables the production fail-closed checks
```

All other dev defaults (`DATABASE_URL`, `KEYCLOAK_URL`, `FLEET_URL`, etc.) are
baked in and match the `docker-compose.yml` service addresses.

For the frontend, `NEXT_PUBLIC_API_URL` defaults to `http://localhost:8080` when
unset in development.

## Running the test suite

```bash
make verify        # fast gate: go vet + unit tests + frontend typecheck/build
make verify-db     # + DB integration tests (starts an ephemeral Postgres)
make verify-all    # + go test -race across all packages
```

## FleetDM enrollment callback (local end-to-end)

The `fleetdm-mock` container will auto-fire the enrollment callback when both
`BACKEND_URL` and `FLEET_WEBHOOK_SECRET` are set in its environment. To test the
full enrollment flow locally, set these in `docker/docker-compose.yml` under the
`fleetdm-mock` service:

```yaml
environment:
  BACKEND_URL: http://host.docker.internal:8080
  FLEET_WEBHOOK_SECRET: dev-secret
```

And start the backend with the matching secret:

```bash
cd backend && APP_ENV=development FLEET_WEBHOOK_SECRET=dev-secret go run cmd/server/main.go
```

## Troubleshooting

**`make dev-up` exits immediately or Keycloak never becomes healthy**

Keycloak takes 30–60 s on first boot. If `make dev-up` returns before Keycloak is
ready, wait a moment then run `make db-migrate` manually and retry `make kc-setup`.

**Backend starts but returns `insecure configuration` errors**

You forgot `APP_ENV=development`. The backend fails closed on any missing or
insecure default unless the env is explicitly set to `development`.

**Frontend shows `NEXT_PUBLIC_API_URL must use https://`**

Only happens if `NODE_ENV=production` is set locally. Use `npm run dev`, not
`npm start`, for local development.

**`make kc-setup` fails with a 401 or connection refused**

Keycloak may not be fully up yet. Wait a few seconds and retry.

**Port conflict**

If 5432, 8081, or 8082 are already in use, stop the conflicting service or edit
`docker/docker-compose.yml` to remap the host ports.

## Next steps

- [docs/ARCHITECTURE.md](ARCHITECTURE.md) — system design and data flows.
- [docs/API.md](API.md) — full API and environment variable reference.
- [docs/DEPLOYMENT.md](DEPLOYMENT.md) — production deployment with TLS.
- [CONTRIBUTING.md](../CONTRIBUTING.md) — how to open a PR.
