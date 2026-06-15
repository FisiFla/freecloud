# Contributing to FreeCloud

Thanks for contributing! This guide covers local setup and the checks your change
must pass.

## Local Setup

```bash
make dev-up            # Postgres + Keycloak + FleetDM mock (Docker)
make db-migrate        # apply migrations
make kc-setup          # create the realm, groups, and freecloud-service client

# Backend (dev defaults require APP_ENV=development; see README)
cd backend && APP_ENV=development go run cmd/server/main.go

# Frontend
cd frontend && npm install && npm run dev
```

Requires Go 1.25+, Node 26, and Docker.

## Checks (run before opening a PR)

```bash
make verify       # CI-required gate: go vet + unit tests + frontend typecheck/test/build
make verify-db    # + DB-backed integration tests (ephemeral Postgres)
make verify-all   # + go test -race across all packages
```

CI additionally runs `staticcheck`, `govulncheck`, `go test -race`, `npm audit`,
and the DB integration suite. To match locally:

```bash
cd backend && go install honnef.co/go/tools/cmd/staticcheck@latest && "$(go env GOPATH)/bin/staticcheck" ./...
cd backend && go install golang.org/x/vuln/cmd/govulncheck@latest && "$(go env GOPATH)/bin/govulncheck" ./...
```

## Conventions

- **Migrations** are append-only: add a new `MigrationNNN` constant and slice
  entry in `backend/internal/db/schema.go`; never edit an applied migration.
- **Security work fails closed.** New config that can be insecure must be
  rejected in production by `config.Validate()`; compat/fallback paths must be
  explicit, never silent.
- **Tests** accompany behavior changes. Handlers can be unit-tested with the
  `fakeDB`/`fakeKeycloak`/`fakeFleet` helpers; DB-touching logic gets an
  `//go:build integration` test exercised by `make test-db`.
- Match the surrounding code style; keep changes surgical.

## Pull Requests

Open PRs against `main`. CI (`verify` + `db-integration`) must be green. Releases
are cut by tagging `vX.Y.Z`, which builds and publishes images to GHCR.
