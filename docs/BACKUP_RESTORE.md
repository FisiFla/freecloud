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
# 0. Take a full backup bundle (database + .secrets/) — preferred.
# Production compose stack must be up; BACKUP_PASSPHRASE is required
# (or pass BACKUP_ARGS=--allow-plaintext for drills/tests only).
BACKUP_PASSPHRASE=... make prod-backup

# 1. Take a database-only backup
DATABASE_URL=postgres://user:pass@host:5432/freecloud \
  scripts/backup.sh /var/backups/freecloud/

# 2a. Verify a bundle (mandatory first step before any restore — aborts
# on any tampering; encrypted bundles need BACKUP_PASSPHRASE and are
# authenticated BEFORE decryption)
BACKUP_PASSPHRASE=... scripts/verify-bundle.sh <bundle> [output-dir]

# 2b. Restore a verified bundle (to a clean target — destructive!)
RESTORE_DATABASE_URL=postgres://user:pass@target:5432/postgres \
  scripts/restore.sh --bundle <verified-bundle-dir>

# 3. Verify a database backup without touching production
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
# Preferred: verify the bundle first, then restore it (inventory-checked).
RESTORE_DATABASE_URL=postgres://user:pass@target:5432/postgres \
  scripts/restore.sh --bundle freecloud-bundle-20260619T020000Z

# Bare dump (no verification — loud warning; only for dumps you trust):
RESTORE_DATABASE_URL=postgres://user:pass@target:5432/postgres \
  scripts/restore.sh freecloud-backup-20260619T020000Z.sql
```

`--bundle` accepts a bundle directory or a `.tar.gz.enc` file (the latter
needs `BACKUP_PASSPHRASE` and is authenticated before decryption). It runs
`scripts/verify-bundle.sh` as a mandatory first step and aborts when the
bundle fails verification.

Every database name the dump would create is validated against
`^[A-Za-z0-9_]+$` **before** the confirmation prompt — a tampered dump is
rejected before it gets anywhere near the database connection.

A `pg_dumpall` script is not expected to run without errors — the bootstrap
superuser already exists on the target ("role already exists"), and the
`--clean` `DROP ROLE IF EXISTS` statements fail whenever the role still owns
objects. The script therefore runs `psql` with `ON_ERROR_STOP=0` instead of
aborting on the first (benign) error, and then **verifies the result**:
- every database the dump creates must exist on the target, and
- the live per-database table counts must match the manifest inventory
  (recorded at backup time) — otherwise the script exits non-zero, because
  `ON_ERROR_STOP=0` would otherwise swallow a failed `CREATE TABLE`
  silently.

Restoring against a target that already has the roles completes
successfully. A bundle whose manifest has no inventory section (taken by
older scripts) restores with a loud warning instead of a silent pass.

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

Hardening notes: cluster-level statements (`CREATE/ALTER/DROP DATABASE`) are
filtered out of the restored sections case-insensitively (SQL keywords execute
in any case), and the stray-database cleanup only drops databases whose names
match `^[A-Za-z0-9_]+$` — anything else is left behind loudly for a human to
inspect, and fails the run.

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
| `manifest.txt` | UTC timestamp, mode, hostname, git HEAD, `FREECLOUD_VERSION`, encryption marker, file sizes, sha256 of `cluster.sql` + `secrets.tar` + `.env.prod`, and the per-database table-count inventory — never any secret values |

The bundle is built in a staging directory and only renamed into place
after the dump validates — a failed run never leaves something that looks
like a good backup. `.secrets/` must exist or the script exits with an error.

```bash
# Production compose stack (must be up) — dumps via the postgres service:
BACKUP_PASSPHRASE=... make prod-backup                       # encrypted (required)
BACKUP_PASSPHRASE=... make prod-backup BACKUP_DIR=/var/backups/freecloud

# Drills/tests only: explicit plaintext opt-out (loud warning, never production):
make prod-backup BACKUP_ARGS=--allow-plaintext

# Direct mode — any reachable PostgreSQL, reuses scripts/backup.sh:
DATABASE_URL=postgres://user:pass@host:5432/freecloud \
BACKUP_PASSPHRASE=... \
  scripts/backup-bundle.sh /var/backups/freecloud
```

### Encryption (`BACKUP_PASSPHRASE`)

The passphrase is **required**: without it the script aborts instead of
writing a plaintext bundle, unless you pass the explicit `--allow-plaintext`
opt-out (which prints a loud warning). The shipped systemd unit provides the
passphrase from a root-only file (see Scheduling below); never put it on a
command line.

An encrypted bundle is published as three files (the plaintext staging copy
is removed):

| File | Contents |
|---|---|
| `freecloud-bundle-<timestamp>.tar.gz.enc` | the bundle, encrypted with `openssl enc -aes-256-cbc -salt -pbkdf2 -iter 600000` |
| `freecloud-bundle-<timestamp>.manifest.txt` | outer copy of the manifest (same content as inside) |
| `freecloud-bundle-<timestamp>.tar.gz.enc.hmac` | HMAC-SHA256 over the ciphertext **and** over the outer manifest (encrypt-then-MAC; key derived from the passphrase under a distinct label) |

Decryption always goes through `scripts/verify-bundle.sh`, which checks both
MACs **before** decrypting anything, then verifies the manifest hashes, the
`secrets.tar` members and the inventory:

```bash
BACKUP_PASSPHRASE=... make prod-backup

# Verify + decrypt (the passphrase is read from the environment):
BACKUP_PASSPHRASE=... scripts/verify-bundle.sh \
  freecloud-bundle-<timestamp>.tar.gz.enc [output-dir]
```

A plaintext bundle (`--allow-plaintext`) is published as
`freecloud-bundle-<timestamp>/` (directory `0700`, files `0600`) and verified
the same way (`scripts/verify-bundle.sh freecloud-bundle-<timestamp>/`),
which checks the sha256 hashes, tar members and inventory in place.

Store the passphrase somewhere that survives the host (password manager) —
without it the encrypted bundle is unrecoverable.

### Legacy bundles (`--legacy`)

Bundles encrypted by older scripts used default PBKDF2 iterations and no
authentication (no `.hmac` sidecar). They are **refused by default** —
decrypt them explicitly, once, to migrate:

```bash
BACKUP_PASSPHRASE=... scripts/verify-bundle.sh --legacy \
  freecloud-bundle-<old-timestamp>.tar.gz.enc [output-dir]
```

The flag is never silent (a loud legacy warning is printed), and the
decrypted content is still hash-checked against whatever the old manifest
records. Re-take the backup with the current scripts afterwards.

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
# lives elsewhere, and create the REQUIRED passphrase file
# /opt/freecloud/.backup-passphrase (mode 0600) containing:
#   BACKUP_PASSPHRASE=<passphrase>
# Without it the unit fails (fail closed — no plaintext bundles by accident).
sudo systemctl daemon-reload
sudo systemctl enable --now freecloud-backup.timer
```

The unit is sandboxed (`NoNewPrivileges`, `ProtectSystem=strict` with the
backups directory as the only writable path, `PrivateTmp`) and documents a
dedicated `freecloud-backup` user with docker access in its header comments —
prefer that over root (see the setup steps in
`docker/systemd/freecloud-backup.service`).

Cron alternative (passphrase from the same root-only file — never inline,
never sourced):

```cron
30 2 * * *  cd /opt/freecloud && scripts/load-secrets-env.sh /opt/freecloud/.backup-passphrase -- make prod-backup >> /var/log/freecloud-backup.log 2>&1
```

### Restore from a bundle on a fresh host

1. Clone the repository at the commit named in `manifest.txt` (`git_head`).
2. **Verify the bundle first (mandatory)** — this aborts on any tampering.
   For an encrypted bundle the MACs are checked before anything is decrypted:
   ```bash
   BACKUP_PASSPHRASE=... scripts/verify-bundle.sh \
     freecloud-bundle-<timestamp>.tar.gz.enc ./restored-bundle
   # — or, for a plaintext bundle directory, verified in place:
   scripts/verify-bundle.sh freecloud-bundle-<timestamp>/
   ```
   Use the verified directory (`./restored-bundle`, or the bundle directory)
   as `BUNDLE` in the steps below.
3. Restore the secrets and config into the checkout — checking the tar
   members **before** extracting (a tar from an untrusted bundle can carry
   `..`, absolute paths or symlinks):
   ```bash
   scripts/check-secrets-tar.sh BUNDLE/secrets.tar \
     && tar -xf BUNDLE/secrets.tar   # recreates .secrets/
   cp BUNDLE/.env.prod .env.prod  # when the bundle has one
   ```
4. Run `make prod-secrets-init` — with the restored `.secrets/` in place this
   is a no-op (the generator is idempotent and never overwrites existing
   secrets), so the restored keys are kept.
5. Start only PostgreSQL (via the safe secrets loader — never source the
   file), then restore the verified bundle:
   ```bash
   scripts/load-secrets-env.sh .secrets/secrets.env -- \
     docker compose -f docker/docker-compose.prod.yml --env-file .env.prod up -d postgres
   RESTORE_DATABASE_URL=postgres://freecloud:<postgres-password>@localhost:5432/postgres \
     scripts/restore.sh --bundle BUNDLE
   ```
   (Point the URL at wherever the new PostgreSQL is reachable; the password
   is `POSTGRES_PASSWORD` from the restored `.secrets/secrets.env`.)
   `restore.sh --bundle` re-runs the verification gate and compares the
   live table counts against the manifest inventory afterwards.
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
public-host restore; it covers the unencrypted bundle shape (explicit
`--allow-plaintext`, since the nightly job covers the encrypted path —
see below); source and target are the same host. The drill runs
`verify-bundle.sh` as a mandatory gate before the wipe and again before the
restore, checks the `secrets.tar` members before extracting, and restores
via `restore.sh --bundle` (manifest-inventory comparison included). Both the
drill and the nightly backup-verify job run from
`.github/workflows/nightly.yml` (schedule + manual dispatch).

### Nightly backup-verify (`scripts/nightly-backup-verify.sh`)

The nightly job proves the **encrypted** path end to end on a schedule: it
boots the prod-shape stack via `prod-smoke.sh`, takes an encrypted bundle
with a throwaway passphrase, authenticates + decrypts it via
`verify-bundle.sh`, checks the manifest carries no secret values, and
restores the dump into a scratch Postgres with `verify-restore.sh`.

### Integrity tests (`scripts/test-backup-integrity.sh`)

`scripts/test-backup-integrity.sh` exercises the backup/restore integrity
logic in isolation (no full stack): fail-closed behaviour, the encrypted
round trip, ciphertext/manifest/`.env.prod` tamper rejection, malicious
database-name and `secrets.tar` rejection, legacy-format handling, and the
restore inventory comparison (the last against a throwaway Postgres
container). Run it after touching any script in this path:

```bash
bash scripts/test-backup-integrity.sh
```

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
