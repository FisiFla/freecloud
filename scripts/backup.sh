#!/usr/bin/env bash
# backup.sh — dump the FreeCloud PostgreSQL cluster to a timestamped SQL file.
#
# Usage:
#   DATABASE_URL=postgres://user:pass@host:5432/freecloud scripts/backup.sh [OUTPUT_DIR]
#
# OUTPUT_DIR defaults to the current directory.
# The dump includes ALL databases in the cluster (freecloud + keycloak).
#
# Requires pg_dumpall on PATH (part of the standard postgresql-client package).
# The dump is plain SQL and contains identity data/secrets. Treat it as sensitive.

set -euo pipefail
umask 077

OUTPUT_DIR="${1:-.}"

if [ -z "${DATABASE_URL:-}" ]; then
  echo "ERROR: DATABASE_URL is not set." >&2
  echo "  Set it to a libpq connection URI, e.g.:" >&2
  echo "  DATABASE_URL=postgres://user:pass@host:5432/freecloud scripts/backup.sh" >&2
  exit 1
fi

# Parse host, port, user, password and database from DATABASE_URL so pg_dumpall
# can connect. pg_dumpall does not accept a connection URI directly; we pass
# individual flags. The password is picked up from PGPASSWORD (set below).
_parsed=$(DATABASE_URL="$DATABASE_URL" python3 - <<'PY'
import os
import urllib.parse as p

u = p.urlparse(os.environ["DATABASE_URL"])
print(u.hostname or 'localhost')
print(u.port or 5432)
print(u.username or 'postgres')
print(u.password or '')
print(u.path.lstrip('/') or 'freecloud')
PY
)
PGHOST=$(echo "$_parsed" | sed -n '1p')
PGPORT=$(echo "$_parsed" | sed -n '2p')
PGUSER=$(echo "$_parsed" | sed -n '3p')
export PGPASSWORD
PGPASSWORD=$(echo "$_parsed" | sed -n '4p')
SRCDB=$(echo "$_parsed" | sed -n '5p')

TIMESTAMP=$(date -u +"%Y%m%dT%H%M%SZ")
OUTFILE="${OUTPUT_DIR}/freecloud-backup-${TIMESTAMP}.sql"
TMPFILE="${OUTFILE}.tmp"

mkdir -p "$OUTPUT_DIR"

# The dump is written to a temporary file first and only published (renamed
# into place) after it has been validated. A failed or truncated dump must
# never leave a file that looks like a valid backup — a cron job would ship
# it off-host as "the backup" and the failure would be invisible.
cleanup() {
  rm -f "${TMPFILE}"
}
trap cleanup EXIT

echo "Backing up PostgreSQL cluster to ${OUTFILE} ..."
if ! pg_dumpall \
  --host="$PGHOST" \
  --port="$PGPORT" \
  --username="$PGUSER" \
  --no-password \
  --clean \
  --if-exists \
  > "${TMPFILE}"; then
  echo "ERROR: pg_dumpall failed — no backup file was written." >&2
  exit 1
fi

if [ ! -s "${TMPFILE}" ]; then
  echo "ERROR: pg_dumpall produced an empty dump — no backup file was written." >&2
  exit 1
fi

# A cluster dump always contains a CREATE DATABASE statement per database.
# The source database must be in there, otherwise the dump does not cover the
# data this script is supposed to protect.
if ! grep -qE "^CREATE DATABASE ${SRCDB}([[:space:]]|;)" "${TMPFILE}"; then
  echo "ERROR: the dump does not contain the '${SRCDB}' database." >&2
  echo "       DATABASE_URL must point at the FreeCloud database. No backup file was written." >&2
  exit 1
fi

chmod 600 "${TMPFILE}"
mv -f "${TMPFILE}" "${OUTFILE}"
trap - EXIT

SIZE=$(du -h "$OUTFILE" | cut -f1)
echo "Backup complete: ${OUTFILE} (${SIZE})"
