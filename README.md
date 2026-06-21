# FreeCloud

Unified, open-source identity and device management. A single pane of glass over Keycloak (Identity Provider / SSO / SCIM) and FleetDM (Device Management / MDM).

## Architecture

- **Backend:** Go + Chi router + gocloak (Keycloak admin client)
- **Frontend:** Next.js 16 (App Router) + React 19 + TypeScript + Tailwind CSS 4
- **Database:** PostgreSQL 16
- **Identity:** Keycloak 25+ (OIDC, SAML, SCIM)
- **MDM:** FleetDM
- **Reverse proxy:** Caddy (production TLS termination)

## Architecture

```mermaid
graph LR
    Browser --> Caddy
    Caddy --> Frontend["Next.js Frontend\n:3000"]
    Caddy --> Backend["Go Backend API\n:8080"]
    Backend --> Keycloak["Keycloak\n(Identity / SSO / SCIM)\n:8081"]
    Backend --> FleetDM["FleetDM\n(MDM)\n:8082 (mock) / real"]
    Backend --> Postgres["PostgreSQL\n:5432"]
    FleetDM -.->|"enrollment callback\nHMAC-SHA256"| Backend
```

### Core Flows

**Employee onboarding / offboarding**

```mermaid
sequenceDiagram
    actor Admin
    participant FE as Frontend
    participant BE as Backend API
    participant KC as Keycloak
    participant Fleet as FleetDM
    participant DB as PostgreSQL

    Admin->>FE: Fill onboard form
    FE->>BE: POST /api/v1/onboard
    BE->>DB: Idempotency pre-check (409 if duplicate)
    BE->>KC: Create user + assign groups/roles
    BE->>Fleet: Mint enrollment token
    BE->>DB: Persist user + enrollment_token (single tx)
    BE-->>FE: enrollmentToken + enrollmentURL
    Note over Fleet,BE: Device enrolls later
    Fleet-->>BE: POST /fleet/enrollment-callback (HMAC signed)
    BE->>DB: Link device ↔ user (consume token)

    Admin->>FE: Trigger offboard
    FE->>BE: POST /api/v1/offboard/{userId}
    BE->>KC: Disable account (502 on failure — fail closed)
    BE->>KC: Terminate all sessions
    BE->>Fleet: Wipe all linked devices
    BE->>DB: Record audit entry
```

**Conditional access (device posture check)**

```mermaid
sequenceDiagram
    actor User
    participant KC as Keycloak
    participant SPI as Keycloak Authenticator SPI
    participant BE as Backend API
    participant Fleet as FleetDM

    User->>KC: SSO login attempt
    KC->>SPI: Evaluate authenticator
    SPI->>BE: POST /api/v1/access/evaluate (ACCESS_EVAL_TOKEN)
    BE->>Fleet: Fetch device posture for user
    BE->>DB: Load per-app access policy
    BE-->>SPI: { allowed: true/false, failures: [...] }
    SPI-->>KC: Grant or deny session
    KC-->>User: Redirect (success) or access-blocked page
```

## Quick Start

```bash
# Start infrastructure
make dev-up

# Run database migrations
make db-migrate

# Start backend (in another terminal). APP_ENV=development opts into the dev
# defaults; without it the server fails closed (treats the env as production).
cd backend && APP_ENV=development go run cmd/server/main.go

# Start frontend (in another terminal)
cd frontend && npm install && npm run dev
```

## Project Structure

```
freecloud/
├── backend/
│   ├── cmd/server/main.go          # Entry point
│   ├── internal/
│   │   ├── config/config.go        # Environment config
│   │   ├── db/schema.go            # Database migrations
│   │   ├── keycloak/client.go      # Keycloak API client
│   │   ├── fleet/client.go         # FleetDM API client
│   │   ├── handlers/               # HTTP handlers
│   │   │   ├── onboarding.go
│   │   │   ├── device_check.go
│   │   │   ├── offboarding.go
│   │   │   ├── apps.go
│   │   │   └── routes.go
│   │   └── middleware/audit.go     # Audit middleware
├── frontend/
│   ├── src/app/
│   │   ├── layout.tsx              # Root layout with sidebar
│   │   ├── page.tsx                # Dashboard home
│   │   ├── employees/              # Employee management
│   │   ├── apps/                   # App Catalog
│   │   ├── audit-log/              # Audit Log viewer
│   │   └── settings/               # System settings
│   └── src/components/             # Reusable UI components
├── docker/
│   └── docker-compose.yml          # Dev infrastructure
├── Makefile
└── README.md
```

## API Endpoints

A full reference is in [docs/API.md](docs/API.md). Quick summary:

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | /api/v1/onboard | Employee onboarding (Keycloak + FleetDM) |
| POST | /api/v1/onboard/bulk | Bulk CSV onboarding |
| POST | /api/v1/offboard/{userId} | Panic button offboarding |
| POST | /api/v1/auth/device-check | Device posture assessment |
| POST | /api/v1/auth/forgot-password | Self-service password reset (public) |
| POST | /api/v1/apps/create | Register SSO application (OIDC or SAML) |
| POST | /api/v1/apps/{appId}/assign | Assign user to app |
| GET/PUT | /api/v1/apps/{appId}/policy | Per-app conditional access policy |
| POST | /api/v1/fleet/enrollment-callback | FleetDM enrollment webhook (HMAC-authenticated) |
| GET | /api/v1/apps | List connected apps |
| GET | /api/v1/users | List users |
| GET/PATCH | /api/v1/users/{id} | Get / update user profile |
| POST | /api/v1/users/{id}/reset-password | Admin password reset |
| GET/POST | /api/v1/groups | List / create Keycloak groups |
| POST/DELETE | /api/v1/users/{id}/groups[/{groupId}] | Assign / unassign user from group |
| GET/POST | /api/v1/roles, /api/v1/users/{id}/roles | List realm roles / assign to user |
| GET/POST | /api/v1/users/{id}/mfa-status, /require-mfa | MFA status and enforcement |
| GET | /api/v1/users/{id}/devices/software | Software inventory per user |
| GET | /api/v1/users/{id}/devices/compliance | Device compliance posture per user |
| GET | /api/v1/compliance | Org-wide compliance summary |
| GET | /api/v1/policies | Fleet policies |
| GET/POST | /api/v1/teams | Fleet teams |
| POST | /api/v1/teams/{id}/policies | Assign policy to team |
| POST | /api/v1/teams/{id}/hosts | Move hosts to team |
| POST | /api/v1/devices/{id}/lock | Remote device lock |
| GET | /api/v1/audit-logs | View audit trail |
| GET | /api/v1/audit-logs/export | Export audit log (CSV or JSON) |
| GET/POST/DELETE | /api/v1/api-tokens | Manage API tokens (super-admin) |
| GET/POST | /api/v1/campaigns | Access review campaigns |
| GET/POST/DELETE | /api/v1/portal/... | Self-service portal |
| POST | /api/v1/access/evaluate | Posture check for conditional access SPI |
| GET | /api/v1/admin/drift | Keycloak↔DB reconciliation drift report |
| GET | /healthz | Liveness probe |
| GET | /readyz | Readiness probe (DB + Keycloak) |
| GET | /api/v1/health | Simple health check |
| GET/GET | /api/v1/health/keycloak, /api/v1/health/fleetdm | Dependency health |
| GET/POST/PATCH/DELETE | /scim/v2/Users, /scim/v2/Groups | SCIM 2.0 provisioning |

### FleetDM enrollment callback

When a host enrolls, FleetDM must `POST /api/v1/fleet/enrollment-callback` so the
device is linked to the user its enrollment token was issued for — that mapping
is what lets offboarding actually lock/wipe the user's devices.

- **Auth:** the request is signed, not JWT-authenticated (Fleet, not a browser,
  calls it). Send `X-Fleet-Signature: sha256=<hex HMAC-SHA256 of the raw body>`
  keyed by `FLEET_WEBHOOK_SECRET`. An unset secret rejects all callbacks.
- **Body:** `{"enrollment_token","host_id","hostname","os_version"}`.
- For local end-to-end testing the `fleetdm-mock` auto-fires this callback when
  `BACKEND_URL` and `FLEET_WEBHOOK_SECRET` are set in its environment.

Both OIDC and SAML applications are fully supported; SAML clients are created with the standard X.500 attribute mappers and SP metadata.

## Development & Testing

The project ships with a tiered Makefile gate so the fast no-live check stays
quick while deeper DB-backed tests are available on demand.

```bash
make verify      # Fast no-live gate: go vet + go test + frontend type-check + build
make test-db     # Ephemeral Postgres (Docker) migration/schema integration tests
make verify-db   # Fast verify + DB integration tests
make verify-all  # Fast verify + DB integration tests + go test -race across all packages
```

`make verify` is the CI-required gate and needs no external services.
`make test-db` starts a throwaway Postgres 16 container (or uses
`TEST_DATABASE_URL` if set), runs the migration suite, and exercises the
schema/user/app/audit/device-mapping queries with the `-race` detector.

Run the backend race tests directly:

```bash
cd backend && go test -race ./internal/handlers ./internal/middleware ./internal/config ./internal/fleet
```

## Production Deployment

A production stack is defined in `docker/docker-compose.prod.yml`: the Go backend
and Next.js dashboard (multi-stage `backend/Dockerfile` and `frontend/Dockerfile`),
a TLS-enabled PostgreSQL, Keycloak in production mode, and **Caddy** as a reverse
proxy that auto-provisions TLS certificates for the public hostnames. There is no
`fleetdm-mock` here — point `FLEET_URL` at a real FleetDM.

```bash
cp .env.prod.example .env.prod      # then fill in real domains + secrets
make prod-up                        # build images + bring the stack up (detached)
```

- **Three public hostnames** (dashboard, API, Keycloak) must resolve to the host;
  Caddy obtains certificates for each.
- The backend **fails closed**: with `APP_ENV=production` it refuses to start on
  default DB credentials, `sslmode=disable`, the `admin-cli` client, a localhost
  Keycloak URL, or any missing secret. Postgres serves TLS so `sslmode=require`
  works on the internal network; the backend runs schema migrations on startup.
- The backend image is `distroless:nonroot`; the frontend runs as a non-root node
  user. `NEXT_PUBLIC_API_URL` is baked at build time from `API_PUBLIC_URL`.

After first boot, run `make kc-setup` against the Keycloak instance to create the
realm, groups, and the `freecloud-service` confidential client (least-privilege:
`manage-users` + `manage-clients`) without creating the development demo user:

```bash
APP_ENV=production ALLOW_DEV_SETUP=true CREATE_DEMO_USER=false make kc-setup
```

## Documentation

- [docs/QUICKSTART.md](docs/QUICKSTART.md) — zero-to-running in under 5 minutes.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — system design, data flows, and security model.
- [docs/API.md](docs/API.md) — full REST API reference and environment variable table.
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) — production deployment, environment
  reference, upgrades, and troubleshooting.
- [docs/BACKUP_RESTORE.md](docs/BACKUP_RESTORE.md) — backup scripts, restore
  runbook, and automated restore verification.
- [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md) — Prometheus metrics, Grafana
  dashboard, and alert rules.
- [SECURITY.md](SECURITY.md) — security model and how to report vulnerabilities.
- [CONTRIBUTING.md](CONTRIBUTING.md) — local setup and the checks a PR must pass.
- [docs/adr/](docs/adr/) — architecture decision records:
  - [0001](docs/adr/0001-distributed-state-integrity.md) distributed-state integrity
  - [0002](docs/adr/0002-fleet-enrollment-callback.md) Fleet enrollment callback
  - [0003](docs/adr/0003-single-instance.md) single-instance v1 constraint

## Operations

### Backup

```bash
DATABASE_URL=postgres://user:pass@host:5432/freecloud \
  scripts/backup.sh /var/backups/freecloud/
```

See [docs/BACKUP_RESTORE.md](docs/BACKUP_RESTORE.md) for the full runbook,
restore instructions, and automated verification.

### Observability

```bash
docker compose \
  -f docker/docker-compose.prod.yml \
  -f docker/docker-compose.observability.yml \
  --env-file .env.prod up -d
```

Grafana dashboard and Prometheus alert rules are in `docker/observability/`.
See [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md).

### Reconciliation drift

The backend runs a background job (interval: `RECONCILE_INTERVAL`, default 15m)
that detects Keycloak↔DB drift and exposes it as Prometheus gauges
(`freecloud_reconcile_orphans_in_keycloak`, `freecloud_reconcile_orphans_in_db`).
An admin API endpoint returns the current drift report on demand:

```
GET /api/v1/admin/drift   (requires JWT auth)
```

Set `RECONCILE_INTERVAL=0` to disable the background job.

### Single-instance note

FreeCloud v1 is designed for a single backend instance (in-memory rate limiter,
no advisory-lock migrations). See
[docs/adr/0003-single-instance.md](docs/adr/0003-single-instance.md) for the
full rationale and what multi-instance would require.

## License

MIT
