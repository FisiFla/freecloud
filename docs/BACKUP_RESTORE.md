# FreeCloud Backup & Restore

PostgreSQL is the authoritative store for user↔Keycloak mappings, the audit
log, device enrollments, and connected-app records. Back it up regularly.
The scripts in `scripts/` automate dump, restore, and restore verification.

## What to back up

The PostgreSQL cluster contains two databases:

| Database | Contents |
|---|---|
| `freecloud` | users, devices, audit logs, app assignments, enrollment tokens |
| `keycloak` | realm config, clients, groups, user credentials |

**Both must be backed up together** — the `keycloak_user_id` foreign key ties the
two systems. Restoring `freecloud` without the matching Keycloak realm produces
orphaned identities.

The `.secrets/` directory must be backed up **with** the database. A database
restore without the matching `PROVISIONING_MASTER_KEY`, `AUTH_SECRET`, and the
other generated secrets is not a working restore (encrypted provisioning
credentials become undecryptable). `scripts/backup-bundle.sh` (below) packages
database + secrets into one artifact; prefer it over a bare database dump.

## Quick reference

```bash
# 0. Take a full backup bundle (database + .secrets/) — preferred
make prod-backup            # production compose stack must be up

# 1. Take a database-only backup
DATABASE_URL=postgres://user:pass@host:5432/freecloud \
  scripts/backup.sh /var/backups/freecloud/

# 2. Restore (to a clean target — destructive!)
RESTORE_DATABASE_URL=postgres://user:pass@target:5432/postgres \
  scripts/restore.sh /var/backups/freecloud/freecloud-backup-YYYYMMDD.sql

# 3. Verify a backup without touching production
DATABASE_URL=postgres://user:pass@host:5432/freecloud \
SCRATCH_DATABASE_URL=postgres://user:pass@scratch:5432/postgres \
  scripts/verify-restore.sh
```

## scripts/backup.sh

Calls `pg_dumpall` to produce a single plain-SQL cluster dump that covers both
databases. The dump is written to `OUTPUT_DIR/freecloud-backup-<timestamp>.sql`
with owner-only permissions (`0600`).

The dump is written to a temporary file first and only published (renamed into
place) after it has been validated: non-empty, and containing a
`CREATE DATABASE <source database>` statement. A failed or truncated dump
therefore never leaves a file that looks like a valid backup — which is what a
cron job would otherwise ship off-host as "the backup".

The dump contains Keycloak realm data, user records, audit logs, API-token hashes,
and enrollment tokens. Treat it as sensitive production data: encrypt it before
copying it off-host and restrict access to the backup directory.

**Requirements:** `pg_dumpall` on `PATH` (part of `postgresql-client`).

```bash
DATABASE_URL=postgres://user:pass@host:5432/freecloud \
  scripts/backup.sh [OUTPUT_DIR]
```

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | yes | libpq URI to the **FreeCloud database** (`freecloud`) in the cluster. The dump covers the whole cluster, but the database named in the URL is the one the script validates the dump against. |
| `OUTPUT_DIR` | no | directory for the dump file (default: `.`) |

Schedule via cron (daily is a good minimum):

```cron
0 2 * * *  DATABASE_URL=postgres://... /opt/freecloud/scripts/backup.sh /var/backups/freecloud/
```

Copy encrypted dumps off-host after creation (S3, rsync to a second machine,
etc.). A backup that lives only on the same disk as the database is not a backup.

## scripts/restore.sh

Restores a cluster dump into a target PostgreSQL instance. The dump was taken
with `--clean --if-exists`, so existing `freecloud` and `keycloak` databases
are dropped and recreated.

**This is destructive.** The script asks for confirmation (`YES`) before
proceeding. Point `RESTORE_DATABASE_URL` at the **postgres** maintenance
database, not the application database.

```bash
RESTORE_DATABASE_URL=postgres://user:pass@target:5432/postgres \
  scripts/restore.sh freecloud-backup-20260619T020000Z.sql
```

A `pg_dumpall` script is not expected to run without errors — the bootstrap
superuser already exists on the target ("role already exists"), and the
`--clean` `DROP ROLE IF EXISTS` statements fail whenever the role still owns
objects. The script therefore runs `psql` with `ON_ERROR_STOP=0` instead of
aborting on the first (benign) error, and then **verifies the result**: every
database the dump creates must exist on the target, otherwise the script exits
non-zero. Restoring against a target that already has the roles completes
successfully.

After restore, restart the backend so it runs any pending schema migrations
against the restored database:

```bash
docker compose restart backend
# or
APP_ENV=... go run cmd/server/main.go
```

## scripts/verify-restore.sh

Restores the dump into a **scratch database** on a throwaway host and compares
row counts against the source. Use this to confirm that backups are actually
restorable — an untested backup is not a backup.

```bash
DATABASE_URL=postgres://user:pass@prod:5432/freecloud \
SCRATCH_DATABASE_URL=postgres://user:pass@scratch:5432/postgres \
  scripts/verify-restore.sh [dump_file.sql]
```

If `dump_file.sql` is omitted the script calls `backup.sh` first to create a
fresh dump.

The dump is a **cluster** dump: it contains `CREATE DATABASE` statements and
`\connect` commands. Feeding it to `psql --dbname=<scratch>` as-is would
create databases named after the production ones on the scratch host and leave
the scratch database empty. The script therefore extracts **only the section
belonging to the source database** and drops the cluster-level statements
before restoring. The scratch database (`freecloud_verify_<pid>`) is dropped
automatically when the script exits, and so is any database that appeared
during the run — the scratch host is left exactly as it was found.

Assertions:
- Row count in `users` matches source
- Row count in `audit_logs` matches source
- Row count in `connected_apps` matches source
- Row count in `devices` matches source
- The dump contains a Keycloak database section (`keycloak`, per
  `docker/postgres/init-keycloak-db.sql`) — a dump without one fails outright
- The restored Keycloak database has `realm` and `user_entity` tables, and at
  least one row in `realm` — a truncated Keycloak section fails here even when
  the `freecloud` section is intact

Exit code `0` = all assertions passed; `1` = one or more mismatches.

## Restore runbook (step by step)

1. **Stop the backend** to prevent writes during restore.
2. Identify the most recent backup file:
   ```bash
   ls -lt /var/backups/freecloud/ | head -5
   ```
3. Confirm the backup is intact by running `verify-restore.sh` against a
   scratch instance before touching production.
4. Set `RESTORE_DATABASE_URL` to the *postgres* maintenance database of the
   target host.
5. Run `scripts/restore.sh <dump_file>` and type `YES` at the prompt.
6. Restart the backend. It will apply any pending schema migrations on startup.
7. Smoke-test: `curl -fsS https://api.example.com/readyz` should return `200`.
8. Log a restore event in your ops runbook and notify the team.

## Keycloak

Keycloak's database is included in the cluster dump (step above). However,
Keycloak also caches config in its own memory. After restoring the `keycloak`
database, restart Keycloak so it reloads from the restored state:

```bash
docker compose restart keycloak
```

## Retention recommendation

Keep at minimum:
- 7 daily dumps on-host
- 4 weekly dumps off-host (object storage or second machine)

Encrypt off-host copies with your normal secrets tooling, for example age, GPG,
or object-storage server-side encryption with tightly scoped IAM access.

Purge old dumps from cron after your retention window:

```bash
find /var/backups/freecloud/ -name "freecloud-backup-*.sql" -mtime +7 -delete
```

## Backup bundles (database + secrets)

`scripts/backup-bundle.sh` produces one artifact that restores a **working**
stack, not just a database:

| File | Contents |
|---|---|
| `cluster.sql` | `pg_dumpall` cluster dump (`freecloud` + `keycloak`), same format as `scripts/backup.sh` |
| `secrets.tar` | tar of `.secrets/` (relative paths) — `PROVISIONING_MASTER_KEY`, `AUTH_SECRET`, DB/admin passwords, per-key secret files |
| `.env.prod` | copy of the production env file, when present (the manifest notes when it is absent) |
| `manifest.txt` | UTC timestamp, mode, hostname, git HEAD, `FREECLOUD_VERSION`, file sizes, and sha256 of `cluster.sql` + `secrets.tar` — never any secret values |

The bundle is built in a staging directory and only renamed into place as
`freecloud-bundle-<UTC timestamp>/` (directory `0700`, files `0600`) after the
dump validates — a failed run never leaves something that looks like a good
backup. `.secrets/` must exist or the script exits with an error.

```bash
# Production compose stack (must be up) — dumps via the postgres service:
make prod-backup                       # = scripts/backup-bundle.sh --compose ./backups
make prod-backup BACKUP_DIR=/var/backups/freecloud

# Direct mode — any reachable PostgreSQL, reuses scripts/backup.sh:
DATABASE_URL=postgres://user:pass@host:5432/freecloud \
  scripts/backup-bundle.sh /var/backups/freecloud
```

### Encryption (`BACKUP_PASSPHRASE`)

When `BACKUP_PASSPHRASE` is non-empty, the bundle is published as a single
encrypted file `freecloud-bundle-<timestamp>.tar.gz.enc` instead of a
directory (the plaintext staging copy is removed), with the manifest beside
it as `freecloud-bundle-<timestamp>.manifest.txt`. The script prints the
exact decrypt command as its final line:

```bash
BACKUP_PASSPHRASE=... make prod-backup

# Decrypt + unpack (the passphrase is read from the environment):
openssl enc -d -aes-256-cbc -pbkdf2 -pass env:BACKUP_PASSPHRASE \
  -in freecloud-bundle-<timestamp>.tar.gz.enc | tar -xzf -
```

Store the passphrase somewhere that survives the host (password manager) —
without it the encrypted bundle is unrecoverable.

### Retention (`BACKUP_RETENTION_DAYS`)

After a successful run, entries in the output directory matching
`freecloud-bundle-*` older than `BACKUP_RETENTION_DAYS` (default `14`) are
pruned and echoed. `0` disables pruning. Nothing with any other name is
touched.

### Scheduling

Systemd units ship in `docker/systemd/` (nightly 02:30 + up to 15m randomized
delay, missed runs fire on boot):

```bash
sudo cp docker/systemd/freecloud-backup.service \
        docker/systemd/freecloud-backup.timer /etc/systemd/system/
# Edit WorkingDirectory=/opt/freecloud in the .service if your checkout
# lives elsewhere. For encrypted bundles, create
# /opt/freecloud/.backup-passphrase (mode 0600) containing:
#   BACKUP_PASSPHRASE=<passphrase>
sudo systemctl daemon-reload
sudo systemctl enable --now freecloud-backup.timer
```

Cron alternative:

```cron
30 2 * * *  cd /opt/freecloud && make prod-backup >> /var/log/freecloud-backup.log 2>&1
```

### Restore from a bundle on a fresh host

1. Clone the repository at the commit named in `manifest.txt` (`git_head`).
2. Decrypt the bundle first if it is a `.tar.gz.enc` (command above).
3. Restore the secrets and config into the checkout:
   ```bash
   tar -xf freecloud-bundle-<timestamp>/secrets.tar   # recreates .secrets/
   cp freecloud-bundle-<timestamp>/.env.prod .env.prod  # when the bundle has one
   ```
4. Run `make prod-secrets-init` — with the restored `.secrets/` in place this
   is a no-op (the generator is idempotent and never overwrites existing
   secrets), so the restored keys are kept.
5. Start only PostgreSQL, then restore the cluster dump with the existing
   restore path:
   ```bash
   set -a && . .secrets/secrets.env && set +a
   docker compose -f docker/docker-compose.prod.yml --env-file .env.prod up -d postgres
   RESTORE_DATABASE_URL=postgres://freecloud:<postgres-password>@localhost:5432/postgres \
     scripts/restore.sh freecloud-bundle-<timestamp>/cluster.sql
   ```
   (Point the URL at wherever the new PostgreSQL is reachable; the password
   is `POSTGRES_PASSWORD` from the restored `.secrets/secrets.env`.)
6. Bring the stack up: `make prod-up`.
7. Smoke-test: `curl -fsS https://api.example.com/readyz` should return `200`,
   and an existing user should be able to sign in (this exercises the restored
   Keycloak realm + secrets together).

### Restore drill (`scripts/restore-drill.sh`)

`scripts/restore-drill.sh` is the executable version of the runbook above: it
boots a provisioned prod-shape stack via `scripts/prod-smoke.sh`, takes a
bundle, wipes everything (containers, volumes, `.secrets/`, `.env.prod`),
restores from the bundle alone, boots the restored stack, and proves the
pre-wipe admin still exists (same Keycloak id, same org name, still
provisioned) and attempts a real Keycloak browser-flow sign-in (authorize →
login form → credentials → `code` redirect). Each of its five stages prints
one `RESULT PASS|FAIL: <stage>` line; the exit code is non-zero on any
failure.

> Known status: stages 1–4 pass; stage 5's browser-flow sign-in currently
> fails on a fresh stack — the restored Keycloak accepts the credentials but
> the posture check denies the login with `user not found or disabled`,
> because the setup-created first admin has no `users` row (only the seeded
> e2e admin gets one; see `backend/cmd/server/main.go`) and no enrolled
> device. Verified pre-existing on the pre-wipe stack, not a restore
> regression. Until the first-admin login path is fixed, the drill proves
> restore continuity up to (but excluding) the final sign-in click.

```bash
bash scripts/restore-drill.sh
```

Requirements: Docker with compose, `curl`, `python3`, free host ports 80/443,
and a checkout whose `.env.prod` (if any) already points at `*.localhost`
(the drill inherits prod-smoke's guard and refuses to touch anything else).
Expect ~15 minutes, most of it image builds on the first run. All Docker work
uses `COMPOSE_PROJECT_NAME` (default `fcrestoredrill`) and is torn down
afterwards, pass or fail; the drill picks a free `/24` for `FREECLOUD_SUBNET`
when you have not exported one, so it coexists with other stacks on the host.
Stage 1 retries prod-smoke once when it fails before any migration completed
(slow-disk first-boot race: the prod postgres healthcheck has no
`start_period`, so `up -d` can abort while initdb is still running); a
post-migration failure is never retried.

```bash
RESTORE_DRILL_KEEP=1 bash scripts/restore-drill.sh   # leave stack + scratch dirs for forensics
COMPOSE_PROJECT_NAME=myproj bash scripts/restore-drill.sh
```

Honest limits: the drill proves the `*.localhost` smoke shape, not a real
public-host restore; it covers the unencrypted bundle only
(`BACKUP_PASSPHRASE` is forced empty); source and target are the same host.
No CI job runs this drill yet — nightly wiring is a follow-up; until then a
local run (quote the `RESULT` lines) is the evidence.

### Offsite copy (operator step)

The bundle scripts deliberately do **not** upload anything — copying bundles
off-host is an operator step, after the (preferably encrypted) bundle exists:

```bash
# rsync to a second machine:
rsync -a --chmod=F600,D700 ./backups/freecloud-bundle-* \
  backup-user@offsite.example.com:/srv/freecloud-backups/

# or rclone to object storage:
rclone copy ./backups/ offsite:freecloud-backups \
  --include 'freecloud-bundle-*'
```

Replace the destinations with your real offsite target. A backup that lives
only on the same disk as the database is not a backup.
