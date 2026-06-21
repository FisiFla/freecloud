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

## Design Tokens & Shared Components

Frontend contributors should be aware of:

- **CSS entry point:** `frontend/src/app/globals.css` — imports Tailwind CSS 4 via
  `@import "tailwindcss"` and loads the v3-compat config via `@config`. Add any
  global CSS resets or base styles here.
- **Tailwind config:** `frontend/tailwind.config.js` — extend themes (colors,
  spacing, font families) here, not with inline styles.
- **Shared components:** `frontend/src/components/`
  - `Sidebar.tsx` — root layout sidebar; includes the dark mode toggle.
  - `ConfirmDialog.tsx` — reusable confirmation modal.
  - `SlideOver.tsx` — slide-over panel for forms.
  - `EmptyState.tsx` — consistent empty-state placeholder.
  - `ErrorBanner.tsx` — error message display.
  - `LoadingRows.tsx` — skeleton loader for table rows.
- **Dark mode:** class-based (`dark:` Tailwind variants). The toggle lives in
  `Sidebar.tsx` and persists the user's preference to `localStorage` under the key
  `theme`. Both modes must meet **WCAG AA contrast** (4.5:1 for body text, 3:1 for
  large text / UI components). Test both when touching colors.
- **Accessibility:** use semantic HTML elements, add `aria-label` attributes to
  icon-only controls, and verify keyboard navigation (Tab order, focus rings, modal
  trap) for any new interactive component.

## Project Structure

The documentation lives in `docs/`:

| File | Contents |
|---|---|
| `docs/QUICKSTART.md` | Zero-to-running guide for new contributors |
| `docs/ARCHITECTURE.md` | System design, data flows, security model, DB schema |
| `docs/API.md` | Full REST endpoint and environment variable reference |
| `docs/DEPLOYMENT.md` | Production deployment runbook |
| `docs/BACKUP_RESTORE.md` | Backup and restore runbook |
| `docs/OBSERVABILITY.md` | Prometheus metrics, Grafana dashboard, alert rules |
| `docs/adr/` | Architecture Decision Records |

When adding a new endpoint, update `docs/API.md`. When making a significant
architecture decision, add an ADR under `docs/adr/`.

## Pull Requests

Open PRs against `main`. CI (`verify` + `db-integration`) must be green. Releases
are cut by tagging `vX.Y.Z`, which builds and publishes images to GHCR.
