# FreeCloud

Unified, open-source alternative to JumpCloud. A single pane of glass over Keycloak (Identity Provider / SSO / SCIM) and FleetDM (Device Management / MDM).

## Architecture

- **Backend:** Go + Chi router + gocloak (Keycloak admin client)
- **Frontend:** Next.js 14 (App Router) + TypeScript + Tailwind CSS
- **Database:** PostgreSQL 16
- **Identity:** Keycloak 25+ (OIDC, SAML, SCIM)
- **MDM:** FleetDM

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

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | /api/v1/onboard | Employee onboarding (Keycloak + FleetDM) |
| POST | /api/v1/offboard/{userId} | Panic button offboarding |
| POST | /api/v1/auth/device-check | Device posture assessment |
| POST | /api/v1/apps/create | Register SSO application |
| POST | /api/v1/apps/{appId}/assign | Assign user to app |
| POST | /api/v1/fleet/enrollment-callback | FleetDM enrollment webhook (HMAC-authenticated) |
| GET | /api/v1/apps | List connected apps |
| GET | /api/v1/audit-logs | View audit trail |
| GET | /api/v1/health | Health check |

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

> Note: SAML app creation is currently a stub (the Keycloak client is created
> without SAML-specific attributes); OIDC apps are fully functional.

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

## License

MIT
