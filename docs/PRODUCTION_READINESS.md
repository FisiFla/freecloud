# FreeCloud production-readiness plan (cycle 25)

Goal: FreeCloud is safe to deploy for real on a single-host VPS (the
deployment shape `docker/docker-compose.prod.yml` already targets), with a
rehearsed upgrade/rollback path, scheduled backups that have been
restore-tested, and no unauthenticated first-admin window.

Source: a 5-lane audit on 2026-11-09/10 (deploy/ops, security, backend
reliability, frontend, CI/release) against `main` @ `612b6b2`, with every
BLOCKER independently spot-checked in code. The recorded backlog
(`.ai_infinite_backlog.md`) had all P1/P2/P3 items already ticked except the
VPS-blocked burn-in; everything below is fresh audit findings, not leftovers.

Out of scope (unchanged architecture limits, see
[docs/PILOT_GO_LIVE.md](PILOT_GO_LIVE.md)): one shared Keycloak realm and one
shared Fleet for all orgs (ADR 0005), no multi-region HA, no Kubernetes —
Docker Compose remains the only packaging.

## Definition of done ("production ready" for the VPS pilot)

1. A fresh deploy cannot be claimed by whoever reaches it first (setup token).
2. The identity realm is hardened by bootstrap, not by Keycloak install
   defaults (brute-force lockout, password policy, bounded sessions, refresh
   rotation) — on new AND pre-existing realms.
3. The deployed bits are the pinned release images (`FREECLOUD_VERSION`), so
   upgrade and rollback are version changes, not rebuilds.
4. `make prod-backup` produces scheduled, restore-verified bundles that
   include the secrets a restore needs to actually work.
5. The production compose stack itself is booted in CI (prod-smoke), so the
   artifact a customer runs is the artifact that was tested.
6. First users never hit raw error plumbing: sign-in explains failures, the
   posture-denied page renders real text, and the operator can prove which
   build is running (`/api/version`).

## Wave 1 — the six blockers + first-user breakage (in flight)

| # | Item | Why it blocks production | Evidence |
|---|------|--------------------------|----------|
| 1 | One-time `SETUP_TOKEN` for `POST /api/v1/setup` | Today the only gate is "no admin exists yet"; first internet visitor owns a fresh deploy | `backend/internal/handlers/setup.go:50` (no token in repo: 0 hits for `SETUP_TOKEN`) |
| 2 | Realm brute-force + password policy + session bounds in bootstrap | Realm inherits Keycloak defaults: `bruteForceProtected=false`, no password policy, 10h+ sessions, reusable refresh tokens | `backend/internal/bootstrap/bootstrap.go:226-254` sets only `AccessTokenLifespan=300` |
| 3 | Prod compose consumes `ghcr.io` release images via `FREECLOUD_VERSION` | Rollback "redeploy previous image tag" was fictional: prod compose has only `build:` for all app services | `docker/docker-compose.prod.yml:56,81,147,162,250`; images published by `.github/workflows/release.yml` never consumed |
| 4 | Backup bundle = PG cluster + `.secrets/` + `.env.prod`, scheduled, restore-verified incl. Keycloak DB | Backups are manual/local; `.secrets` is not backed up (a restored DB without `PROVISIONING_MASTER_KEY` is dead); `verify-restore.sh` ignores the Keycloak DB section | `docs/BACKUP_RESTORE.md:7,165`; `scripts/backup.sh` dumps only PG |
| 5 | `accessBlocked` message bundle for the posture SPI | Every posture denial renders the unresolved key (`???accessBlocked???`) as title/header; posture defaults ON in prod | `access-blocked.ftl:4,6`; no `messages*.properties` exists in `keycloak-authenticator` |
| 6 | Frontend first-run/sign-in honesty | `?error=auth_failed`/`session_expired` silently dropped by the sign-in page; backend-down sends `/setup` to a login for a nonexistent admin; no frontend health/version surface | `frontend/src/app/signin/page.tsx:6`, `frontend/src/proxy.ts:37`, `frontend/Dockerfile:21` |

Lane split (non-overlapping files, parallel PRs):

- **Backend auth** (#1, #2): config + handler + bootstrap + `generate.sh` +
  prod secret wiring + setup-page token field. Token semantics: required
  when configured (403 on mismatch, constant-time compare); production
  without a token fails closed (503); dev/test stacks unchanged. Realm
  values are Keycloak's own admin-console template (failureFactor 30 etc.),
  `length(12)` matching `minAdminPasswordLength`, SSO max aligned with the
  frontend session max age, refresh rotation on with zero reuse.
- **SPI** (#5): `messages_en.properties` + ftl `msg()` for body strings +
  a test asserting every template key has a bundle entry.
- **Frontend** (#6): sign-in error surfacing, tri-state provisioned check,
  502/503/504 + 401 mapping in `api.ts`, `/api/health`, `/api/version`
  (GIT_SHA/VERSION build args), Dockerfile healthcheck, exact `next-auth`
  pin, dead `NEXT_PUBLIC_API_URL` removed.
- **Backup/DR** (#4): `scripts/backup-bundle.sh` (+`--compose` mode,
  optional openssl encryption, retention), `verify-restore.sh` two-DB
  assertions, systemd units, `make prod-backup`.
- **Deploy** (#3): `image:` pins + `prod-pull` + Updating/Rollback rewrite +
  dev-stack URL-form `KC_HOSTNAME` (last v1-form stack).

## Wave 2 — proof + remaining majors (next, same cycle)

| # | Item | Notes |
|---|------|-------|
| 7 | **Prod-smoke CI job**: boot `docker-compose.prod.yml` (dummy `.env.prod`, fake hosts, Caddy internal TLS) and assert `/healthz` + `/readyz` through Caddy + KC issuer; gate `release.yml` on Verify | The production artifact has never been booted by any automation (`.github/workflows/e2e.yml:74` boots only e2e compose). Sequences after Wave 1 so it can exercise the setup token + pinned images |
| 8 | Rate-limit SCIM + access-evaluate ahead of bearer auth; reject placeholder/1-char secrets in prod `Validate` | Audit security lane: those groups are unthrottled today |
| 9 | JWKS stale-cache fallback (serve valid tokens during a KC blip instead of a 401 cohort) + Redis in `/readyz` | Audit reliability lane |
| 10 | Bounded timeouts: gocloak client + bootstrap HTTP client; migration context ≥10m; `stop_grace_period: 35s` to match the 30s graceful shutdown | Compose currently kills at 10s mid-drain |
| 11 | Compose/Caddy hardening: healthchecks + `service_healthy` edges, log rotation, mem/cpu limits, Grafana bound to 127.0.0.1, Keycloak `/admin/*` + `/realms/master/*` blocked at Caddy by default | KC admin console is internet-reachable via the KC vhost today; sequenced after `/api/health` (Wave 1 frontend) |
| 12 | Browser-level sign-in e2e on the localhost stack | The #286 bug class (real browser redirect loop) still has no automated browser test |
| 13 | Release hygiene: SBOM/provenance attestations, per-tag GitHub Release with notes, Dependabot docker subdirs, nightly backup-verify + prev-tag upgrade + KC rehearsal jobs | `release.yml` today: build+push only |
| 14 | `_FILE` delivery for `FLEET_API_TOKEN` and the PG password inside `DATABASE_URL` | Last plaintext secrets visible in `docker inspect` on the prod backend |

## Operator-gated (cannot be closed from the dev box)

- **Real VPS deploy + burn-in** (backlog's one remaining P3): purchase
  deferred to the Black Friday window; then: first real deploy by
  `docs/DEPLOYMENT.md`, restore drill on the host, rollback drill
  (`FREECLOUD_VERSION` down + up), 2-replica soak, Caddy `/admin` allowlist
  to operator IP/VPN, backup timer installed with an offsite destination
  and `BACKUP_PASSPHRASE` set, load baseline recorded.
- **Live integrations**: real FleetDM instance (prod stack has no mock),
  GitHub/Slack SCIM sandbox credentials, live Slack alert webhook test.
- **GitHub settings**: confirm branch-protection required checks and GHCR
  package visibility (not determinable from repo files).
- **Production Keycloak 25 dump migration**: rehearsal used a synthesized
  DB (spike 0002); a real long-lived 25 dump remains unproven, as does an
  executed rollback restore.

## Decisions taken while planning (deliberate, auditable)

- **SPI fail-open stays an operator escape hatch.** The audit suggested
  failing logins closed when posture is disabled outside dev/test. Kept as
  is: posture defaults ON in prod compose and the SPI logs a SECURITY
  warning when disabled (cycle 18); hard-failing would brick logins on a
  config omission with no recovery path short of redeploy. Wave 1 fixes
  only the broken denied-page text.
- **`RefreshTokenMaxReuse=0`** (strict rotation) despite the theoretical
  concurrent-refresh race: the BFF refreshes only near expiry and Auth.js
  serializes per session; CI e2e is the acceptance test. Revisit to 1 only
  with evidence of false logouts.
- **Custom Postgres image stays build-on-host** (`freecloud-postgres:local`):
  small TLS wrapper, changes rarely, not worth a fifth published image.
  Noted in DEPLOYMENT so rollbacks don't pretend to version it.
- **i18n beyond the message bundle is deferred**: setup/sign-in/blocked UI
  strings remain English-only literals (no i18n framework in the frontend).
  Acceptable for the pilot; introducing `next-intl` is its own project.

## Acceptance checklist for declaring the pilot production ready

- [ ] Wave 1 merged, `main` Verify + E2E green.
- [ ] Wave 2 merged; prod-smoke job green and required.
- [ ] Local simulator re-proven at the new `main`: fresh-stack setup
      requires the token, browser sign-in works, posture denial renders
      "Access blocked".
- [ ] Backup bundle drill on the dev box: bundle → wipe → restore → login
      works (app + Keycloak), timer files installed.
- [ ] Upgrade/rollback drill on the simulator between two pinned versions.
- [ ] Operator-gated list handed over with the VPS purchase decision; the
      only remaining items are those requiring a real host, real DNS, or
      live third-party credentials.
