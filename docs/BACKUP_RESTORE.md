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

## Quick reference

```bash
# 1. Take a backup
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
| `DATABASE_URL` | yes | libpq URI to any database in the cluster |
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
fresh dump. The scratch database (`freecloud_verify_<pid>`) is dropped
automatically when the script exits.

Assertions:
- Row count in `users` matches source
- Row count in `audit_logs` matches source
- Row count in `connected_apps` matches source
- Row count in `devices` matches source

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
