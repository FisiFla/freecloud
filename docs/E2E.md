# E2E Test Harness

## Overview

The e2e harness spins up a self-contained Docker Compose stack and runs Go tests
tagged `//go:build e2e` against the real backend.

**Components:**

| Service       | Port (host) | Purpose                              |
|---------------|-------------|--------------------------------------|
| postgres-e2e  | 55433       | FreeCloud + Keycloak DB              |
| keycloak-e2e  | 8083        | Real Keycloak (start-dev mode)       |
| fleetdm-e2e   | 8084        | FleetDM mock (extended for B2/B3)    |
| backend-e2e   | 8085        | FreeCloud backend (all migrations)   |

## Prerequisites

- Docker (>= 20.10) and docker compose plugin
- Go 1.22+

## Running locally

```bash
# 1. Start the stack (from repo root)
docker compose -f docker/docker-compose.e2e.yml up -d --build

# 2. Wait for the backend to be ready (≈30s on a cold start)
until curl -sf http://localhost:8085/healthz; do sleep 2; done

# 3. Run the tests
cd backend && go test -tags=e2e -v -timeout 5m ./internal/e2e/... \
  -backend=http://localhost:8085 \
  -scim-token=e2e-scim-token \
  -webhook-secret=e2e-webhook-secret

# 4. Tear down
docker compose -f docker/docker-compose.e2e.yml down -v
```

Environment variable fallbacks (alternative to flags):

```bash
export E2E_BACKEND_URL=http://localhost:8085
export E2E_SCIM_TOKEN=e2e-scim-token
export E2E_WEBHOOK_SECRET=e2e-webhook-secret
cd backend && go test -tags=e2e -v ./internal/e2e/...
```

## What is covered

| Test | Flow |
|------|------|
| `TestE2E_Health` | Backend liveness probe |
| `TestE2E_SCIMUsers_Lifecycle` | SCIM /Users: create → get → list → patch (deactivate) → delete |
| `TestE2E_SCIMGroups_Lifecycle` | SCIM /Groups: create → get → list → patch (rename) → delete |
| `TestE2E_Onboard_EnrollmentCallback_Offboard` | Fleet enrollment callback HMAC verification |
| `TestE2E_FleetTeams_Policies` | Teams endpoint reachability + auth gate |
| `TestE2E_AppCreateStub` | Apps endpoint reachability + auth gate |
| `TestE2E_CompliancePosure` | Compliance endpoint reachability + auth gate |

## CI

The `.github/workflows/e2e.yml` workflow runs:
- On `workflow_dispatch` (manual trigger)
- Nightly at 02:00 UTC

It is intentionally excluded from the PR fast gate (`verify.yml`) because it
requires Docker and takes several minutes. Merge on green `verify` + `db-integration`;
`e2e` catches regressions overnight.

## Notes

- Auth-gated endpoints (onboard, offboard, etc.) are not fully exercised by the
  e2e suite without a live Keycloak realm configured with a client that issues
  JWTs. The SCIM flows (bearer-token only) and the enrollment callback (HMAC)
  are fully exercisable without OAuth.
- To test JWT-gated flows, use the self-bootstrapped e2e Keycloak realm and obtain
  a token from the realm's token endpoint.
