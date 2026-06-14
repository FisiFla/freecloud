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

# Start backend (in another terminal)
cd backend && go run cmd/server/main.go

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
| GET | /api/v1/apps | List connected apps |
| GET | /api/v1/audit-logs | View audit trail |
| GET | /api/v1/health | Health check |

## Development & Testing

The project ships with a tiered Makefile gate so the fast no-live check stays
quick while deeper DB-backed tests are available on demand.

```bash
make verify      # Fast no-live gate: go vet + go test + frontend type-check + build
make test-db     # Ephemeral Postgres (Docker) migration/schema integration tests
make verify-db   # Fast verify + DB integration tests
make verify-all  # Fast verify + go test -race across all packages
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
