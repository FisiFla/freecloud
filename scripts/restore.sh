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

_parsed=$(python3 -c "
import sys, urllib.parse as p
u = p.urlparse('$RESTORE_DATABASE_URL')
print(u.hostname or 'localhost')
print(u.port or 5432)
print(u.username or 'postgres')
print(u.password or '')
print(u.path.lstrip('/') or 'postgres')
")
PGHOST=$(echo "$_parsed" | sed -n '1p')
PGPORT=$(echo "$_parsed" | sed -n '2p')
PGUSER=$(echo "$_parsed" | sed -n '3p')
export PGPASSWORD
PGPASSWORD=$(echo "$_parsed" | sed -n '4p')
PGDB=$(echo "$_parsed" | sed -n '5p')

echo "Restoring ${DUMP_FILE} into ${PGHOST}:${PGPORT} (database: ${PGDB}) ..."
echo "WARNING: existing databases in the dump will be dropped and recreated."
read -r -p "Type YES to continue: " CONFIRM
if [ "$CONFIRM" != "YES" ]; then
  echo "Aborted." >&2
  exit 1
fi

psql \
  --host="$PGHOST" \
  --port="$PGPORT" \
  --username="$PGUSER" \
  --dbname="$PGDB" \
  --no-password \
  --file="$DUMP_FILE" \
  --set ON_ERROR_STOP=1

echo "Restore complete."
