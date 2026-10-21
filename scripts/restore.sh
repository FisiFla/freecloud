#!/usr/bin/env bash
# restore.sh — restore a FreeCloud PostgreSQL dump into a target database.
#
# Usage:
#   RESTORE_DATABASE_URL=postgres://user:pass@host:5432/postgres \
#     scripts/restore.sh <dump_file.sql>
#
# RESTORE_DATABASE_URL must connect to the *postgres* maintenance database
# (not freecloud/keycloak), because the dump may drop and recreate those DBs.
#
# WARNING: This restores the whole cluster dump produced by backup.sh. It will
# DROP existing databases named in the dump (--clean was passed at dump time).
# Only run this against a dedicated restore target — never against production
# unless you mean to replace it entirely.
#
# For a safe restore test, use verify-restore.sh instead.

set -euo pipefail

DUMP_FILE="${1:-}"
if [ -z "$DUMP_FILE" ]; then
  echo "ERROR: provide the dump file as the first argument." >&2
  echo "  Usage: RESTORE_DATABASE_URL=... scripts/restore.sh freecloud-backup-YYYYMMDD.sql" >&2
  exit 1
fi

if [ ! -f "$DUMP_FILE" ]; then
  echo "ERROR: dump file not found: $DUMP_FILE" >&2
  exit 1
fi

if [ -z "${RESTORE_DATABASE_URL:-}" ]; then
  echo "ERROR: RESTORE_DATABASE_URL is not set." >&2
  echo "  Point it at the postgres maintenance database of your restore target:" >&2
  echo "  RESTORE_DATABASE_URL=postgres://user:pass@host:5432/postgres scripts/restore.sh dump.sql" >&2
  exit 1
fi

_parsed=$(RESTORE_DATABASE_URL="$RESTORE_DATABASE_URL" python3 - <<'PY'
import os
import urllib.parse as p

u = p.urlparse(os.environ["RESTORE_DATABASE_URL"])
print(u.hostname or 'localhost')
print(u.port or 5432)
print(u.username or 'postgres')
print(u.password or '')
print(u.path.lstrip('/') or 'postgres')
PY
)
PGHOST=$(echo "$_parsed" | sed -n '1p')
PGPORT=$(echo "$_parsed" | sed -n '2p')
PGUSER=$(echo "$_parsed" | sed -n '3p')
export PGPASSWORD
PGPASSWORD=$(echo "$_parsed" | sed -n '4p')
PGDB=$(echo "$_parsed" | sed -n '5p')

psql_target() {
  psql \
    --host="$PGHOST" \
    --port="$PGPORT" \
    --username="$PGUSER" \
    --dbname="$PGDB" \
    --no-password \
    -X \
    --tuples-only \
    --no-align \
    "$@"
}

echo "Restoring ${DUMP_FILE} into ${PGHOST}:${PGPORT} (database: ${PGDB}) ..."
echo "WARNING: existing databases in the dump will be dropped and recreated."
read -r -p "Type YES to continue: " CONFIRM
if [ "$CONFIRM" != "YES" ]; then
  echo "Aborted." >&2
  exit 1
fi

PSQL_LOG="$(mktemp -t freecloud-restore.XXXXXX)"
cleanup() {
  rm -f "${PSQL_LOG}"
}
trap cleanup EXIT

# A pg_dumpall cluster dump is NOT expected to run without errors: the
# bootstrap superuser already exists on the target ("role ... already
# exists"), and because the dump was taken with --clean it also emits
# DROP ROLE IF EXISTS, which fails whenever the role still owns objects.
# Aborting on the first such error would leave a half-restored cluster, so
# psql runs with ON_ERROR_STOP=0 and the result is verified afterwards.
# -X keeps ~/.psqlrc out of the way so local psql settings cannot change the
# behaviour of a production restore.
if ! psql \
  --host="$PGHOST" \
  --port="$PGPORT" \
  --username="$PGUSER" \
  --dbname="$PGDB" \
  --no-password \
  -X \
  --set ON_ERROR_STOP=0 \
  --file="$DUMP_FILE" 2>&1 | tee "${PSQL_LOG}"; then
  echo "" >&2
  echo "ERROR: psql aborted while restoring the dump — the target is incomplete." >&2
  exit 1
fi

# ── Verify the result ────────────────────────────────────────────────────────
#
# "psql exited 0" only means it did not hit a fatal error. Check that every
# database the dump creates actually exists on the target now.

EXPECTED_DBS=$(sed -n 's/^CREATE DATABASE \([^ ]*\).*/\1/p' "$DUMP_FILE" | tr -d '"' | sort -u)
if [ -z "${EXPECTED_DBS}" ]; then
  echo "WARNING: the dump contains no CREATE DATABASE statements — nothing to verify." >&2
fi

FAILED=0
for db in ${EXPECTED_DBS}; do
  count=$(psql_target -c "SELECT count(*) FROM pg_database WHERE datname = '${db}';" | tr -d '[:space:]')
  if [ "${count}" = "1" ]; then
    echo "  OK  database ${db} exists"
  else
    echo "  FAIL database ${db} is missing after the restore" >&2
    FAILED=1
  fi
done

if [ "${FAILED}" -ne 0 ]; then
  echo "" >&2
  echo "Restore finished with errors: the restored cluster is incomplete." >&2
  echo "See the psql output above for the failing statements." >&2
  exit 1
fi

echo "Restore complete."
