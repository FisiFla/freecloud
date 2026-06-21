#!/usr/bin/env bash
# backup.sh — dump the FreeCloud PostgreSQL cluster to a timestamped SQL file.
#
# Usage:
#   DATABASE_URL=postgres://user:pass@host:5432/db scripts/backup.sh [OUTPUT_DIR]
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
  echo "  DATABASE_URL=postgres://user:pass@host:5432/db scripts/backup.sh" >&2
  exit 1
fi

# Parse host, port, and user from DATABASE_URL so pg_dumpall can connect.
# pg_dumpall does not accept a connection URI directly; we pass individual flags.
# The password is picked up from PGPASSWORD (set below).
_parsed=$(DATABASE_URL="$DATABASE_URL" python3 - <<'PY'
import os
import urllib.parse as p

u = p.urlparse(os.environ["DATABASE_URL"])
print(u.hostname or 'localhost')
print(u.port or 5432)
print(u.username or 'postgres')
print(u.password or '')
PY
)
PGHOST=$(echo "$_parsed" | sed -n '1p')
PGPORT=$(echo "$_parsed" | sed -n '2p')
PGUSER=$(echo "$_parsed" | sed -n '3p')
export PGPASSWORD
PGPASSWORD=$(echo "$_parsed" | sed -n '4p')

TIMESTAMP=$(date -u +"%Y%m%dT%H%M%SZ")
OUTFILE="${OUTPUT_DIR}/freecloud-backup-${TIMESTAMP}.sql"

mkdir -p "$OUTPUT_DIR"

echo "Backing up PostgreSQL cluster to ${OUTFILE} ..."
pg_dumpall \
  --host="$PGHOST" \
  --port="$PGPORT" \
  --username="$PGUSER" \
  --no-password \
  --clean \
  --if-exists \
  > "$OUTFILE"
chmod 600 "$OUTFILE"

SIZE=$(du -h "$OUTFILE" | cut -f1)
echo "Backup complete: ${OUTFILE} (${SIZE})"
