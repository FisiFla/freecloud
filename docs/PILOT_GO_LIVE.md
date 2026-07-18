# FreeCloud pilot go-live checklist

Use this for a **careful first production or laptop pilot**. It is not a
substitute for multi-region HA or realm-per-org isolation.

## Architecture limits (read before go-live)

| Area | Reality in FreeCloud today |
|------|----------------------------|
| Keycloak | **One shared realm** for all orgs. User emails are **unique realm-wide**, not per-org. See ADR 0005. |
| FleetDM | **One shared Fleet**. Teams are org-mapped via `fleet_team_orgs` and namespaced `{org_id}/{name}` on create. Unmapped legacy teams are invisible to non–system-admins. |
| Isolation | Enforced in FreeCloud Postgres + API/SCIM/UI, **not** by separate identity realms or separate Fleet tenants. |
| HA | Leader-elected background jobs (SIEM, schedules, reconcile, snapshots) are implemented; multi-replica soak is operator-owned. |

If you need hard tenant isolation (separate credentials/namespaces per customer),
do **not** claim FreeCloud is realm-per-org ready — that work is deferred.

## Local laptop pilot (Docker)

```bash
# Prerequisites: Docker (or Colima) running

# Generate geoip fixture required by e2e compose (once)
mkdir -p backend/.e2e-fixtures
go run ./backend/cmd/genfixturemmdb -out backend/.e2e-fixtures/geoip-fixture.mmdb

# Full localhost stack (dashboard :3000, API :8080, KC :8081, Fleet mock :8082)
make localhost-up
# or purpose-built e2e stack (API :8085, KC :8083, Fleet mock :8084)
docker compose -f docker/docker-compose.e2e.yml up -d --build

# Health
curl -sf http://localhost:8085/healthz   # e2e backend
# curl -sf http://localhost:8080/healthz # localhost stack

# Automated e2e (against e2e compose)
cd backend && go test -tags=e2e -timeout 10m ./internal/e2e/ \
  -backend=http://localhost:8085 \
  -scim-token=e2e-scim-token \
  -webhook-secret=e2e-webhook-secret \
  -keycloak-url=http://localhost:8083

# Teardown
docker compose -f docker/docker-compose.e2e.yml down -v
# make localhost-down
```

## Smoke checklist (any deploy)

1. **Health** — `GET /healthz` → 200.
2. **Setup locked** — after bootstrap, `POST /api/v1/setup` returns already-provisioned / 409.
3. **Admin JWT** — login (or e2e admin seed) can call `GET /api/v1/users`.
4. **SCIM** — create user with bearer token; get/list/patch/delete.
5. **Enrollment** — onboard → Fleet webhook HMAC → device mapped.
6. **Posture** — access/evaluate denies missing device; allows enrolled compliant host.
7. **Fleet teams** — create team → row in `fleet_team_orgs` → ListTeams returns team.
8. **Backup** — `pg_dumpall` of FreeCloud Postgres cluster; restore into empty instance; count tables.

## Backup / restore (verified against e2e Postgres)

```bash
# Dump (cluster)
docker compose -f docker/docker-compose.e2e.yml exec -T postgres-e2e \
  pg_dumpall -U freecloud --clean --if-exists > freecloud-backup.sql

# Restore into a throwaway Postgres and verify fleet_team_orgs count
docker run -d --name freecloud-restore-verify -e POSTGRES_PASSWORD=restore postgres:16-alpine
# wait for ready, then:
docker exec -i freecloud-restore-verify psql -U postgres < freecloud-backup.sql
docker exec freecloud-restore-verify \
  psql -U freecloud -d freecloud_e2e -c 'SELECT count(*) FROM fleet_team_orgs;'
docker rm -f freecloud-restore-verify
```

Production operators should use `scripts/backup.sh` / `scripts/restore.sh` and
encrypt dumps off-host (see `docs/BACKUP_RESTORE.md`).

## Production host (real VPS) — after laptop pilot

1. Copy `.env.prod.example` → `.env.prod` (real domains + Fleet URL/token).
2. `make prod-secrets-init` then `make prod-up`.
3. Point DNS at the host; confirm Caddy TLS.
4. Backfill legacy Fleet teams into `fleet_team_orgs` (SQL in DEPLOYMENT.md).
5. Run the smoke checklist above against public URLs.
6. Schedule daily backups + one restore drill on a scratch DB.
7. Optional: dual-replica with shared Postgres/Redis; confirm only one leader per job.

## Explicitly not required for pilot

- Realm-per-org Keycloak isolation
- Live GitHub/Slack SCIM sandbox credentials
- Hard npm-audit gate / CSP nonce (recommended soon after pilot)
