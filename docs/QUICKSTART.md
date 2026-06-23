# Quickstart

Get FreeCloud running locally in under 5 minutes.

## Prerequisites

- Docker 24+ with the Compose plugin (`docker compose version`)
- Go 1.25+ and Node.js 26+ (only needed if you run the backend/frontend **outside** Docker)

## 1. Clone

```bash
git clone https://github.com/FisiFla/freecloud.git
cd freecloud
```

## 2. Start the all-in-one stack

```bash
docker compose -f docker/docker-compose.localhost.yml up --build
```

This single command:

- Generates all secrets automatically on first boot (no manual env setup).
- Starts Postgres, Keycloak, the FleetDM mock, the backend, and the frontend.
- The backend self-bootstraps the Keycloak realm on startup — no `make kc-setup` or
  manual client-secret steps required.

Wait for the stack to be ready — you will see the backend log `migrations completed`
and then `server started`. The whole sequence usually takes under 90 s on first boot.

## 3. Open the setup wizard

Open [http://localhost:3000](http://localhost:3000).

If this is a fresh database you will be redirected to `/setup` automatically.
Fill in your admin email, a strong password, and an organisation name, then click
**Create admin account**. That is the only manual step.

If you are returning to an existing database (already provisioned), the wizard
is skipped and you land on the login page.

## 4. Sign in

Use the admin credentials you just created. After sign-in you land on the
FreeCloud dashboard.

## Ports at a glance

| Service | URL |
|---|---|
| Frontend (dashboard) | http://localhost:3000 |
| Backend API | http://localhost:8080 |
| Keycloak admin console | http://localhost:8081 |
| FleetDM mock | http://localhost:8082 |

## How secrets work

On first boot a `secrets-init` container generates random values for all tokens
and writes them to `.secrets/secrets.env`. Every other container loads that file
automatically. You never need to set `SCIM_BEARER_TOKEN`, `AUTH_SECRET`,
`FLEET_WEBHOOK_SECRET`, or `ACCESS_EVAL_TOKEN` by hand.

To rotate all secrets, delete `.secrets/secrets.env` and restart the stack.

## Configuring Fleet, SMTP, and Identity Providers

All integration settings are in-app under **Settings**:

- **Settings → Fleet** — Fleet API URL and token.
- **Settings → SMTP** — outbound email (notifications, invites).
- **Settings → Identity Providers** — add OIDC/SAML federation sources (Google
  Workspace, Azure AD, Okta, …).

There are no corresponding env-var knobs to set by hand.

## Running the test suite

```bash
make verify        # fast gate: go vet + unit tests + frontend typecheck/build
make verify-db     # + DB integration tests (starts an ephemeral Postgres)
make verify-all    # + go test -race across all packages
```

## Troubleshooting

**Keycloak never becomes healthy / backend keeps retrying**

Keycloak takes 30–60 s on first boot. The backend retries the admin login
automatically — just wait. If it still does not come up after 3 min, check
`docker compose -f docker/docker-compose.localhost.yml logs keycloak`.

**`localhost:3000` is stuck on a loading spinner**

The frontend requires `AUTH_SECRET` from `.secrets/secrets.env`. If the file was
not generated (e.g. the `secrets-init` container failed), delete `.secrets/` and
restart with `docker compose ... down -v && docker compose ... up --build`.

**Port conflict (3000, 8080, 8081, 8082)**

Edit `docker/docker-compose.localhost.yml` to remap the host ports, or stop the
conflicting service.

**`setup/status` returns `provisioned: false` unexpectedly**

The backend self-bootstraps when it can reach Keycloak. If it started before
Keycloak was up it will keep retrying in the background; `GET
/api/v1/setup/status` will flip to `true` once bootstrap completes. Refresh the
browser after a few seconds.

## Next steps

- [docs/ARCHITECTURE.md](ARCHITECTURE.md) — system design and data flows.
- [docs/API.md](API.md) — full API and environment variable reference.
- [docs/DEPLOYMENT.md](DEPLOYMENT.md) — production deployment with TLS.
- [CONTRIBUTING.md](../CONTRIBUTING.md) — how to open a PR.
