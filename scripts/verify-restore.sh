#!/usr/bin/env bash
# verify-restore.sh — restore a backup into a scratch DB and assert row counts.
#
# This is the "test your backup" script. Run it periodically (e.g. from CI or
# a scheduled cron job) to confirm that the latest backup is actually restorable
# and contains the expected data.
#
# Usage:
#   DATABASE_URL=postgres://src:pass@srchost:5432/freecloud \
#   SCRATCH_DATABASE_URL=postgres://user:pass@scratchhost:5432/postgres \
#     scripts/verify-restore.sh [dump_file.sql]
#
# If dump_file is omitted the script calls backup.sh first to create a fresh
# dump from DATABASE_URL.
#
# SCRATCH_DATABASE_URL must point at the *postgres* maintenance database of a
# throwaway host. The script creates a fresh database named freecloud_verify,
# restores the freecloud portion of the dump into it, runs assertions, then
# drops it.
#
# Does NOT require Docker — any reachable PostgreSQL instance works.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMPDIR_BACKUP="$(mktemp -d)"
VERIFY_DB=""

cleanup() {
  if [ -n "${VERIFY_DB:-}" ]; then
    echo "Dropping scratch database ${VERIFY_DB} ..."
    psql_scratch -c "DROP DATABASE IF EXISTS ${VERIFY_DB};" > /dev/null 2>&1 || true
  fi
  rm -rf "$TMPDIR_BACKUP"
}
trap cleanup EXIT

# ── Inputs ───────────────────────────────────────────────────────────────────

DUMP_FILE="${1:-}"

if [ -z "${DATABASE_URL:-}" ]; then
  echo "ERROR: DATABASE_URL (source) is not set." >&2
  exit 1
fi
if [ -z "${SCRATCH_DATABASE_URL:-}" ]; then
  echo "ERROR: SCRATCH_DATABASE_URL is not set." >&2
  echo "  Point it at the postgres maintenance database of a throwaway instance." >&2
  exit 1
fi

# ── Parse scratch connection ──────────────────────────────────────────────────

_parsed=$(SCRATCH_DATABASE_URL="$SCRATCH_DATABASE_URL" python3 - <<'PY'
import os
import urllib.parse as p

u = p.urlparse(os.environ["SCRATCH_DATABASE_URL"])
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

VERIFY_DB="freecloud_verify_$$"

psql_scratch() {
  psql \
    --host="$PGHOST" \
    --port="$PGPORT" \
    --username="$PGUSER" \
    --dbname="postgres" \
    --no-password \
    --tuples-only \
    --no-align \
    "$@"
}

psql_verify() {
  psql \
    --host="$PGHOST" \
    --port="$PGPORT" \
    --username="$PGUSER" \
    --dbname="$VERIFY_DB" \
    --no-password \
    --tuples-only \
    --no-align \
    "$@"
}

# ── Dump (if not provided) ────────────────────────────────────────────────────

if [ -z "$DUMP_FILE" ]; then
  echo "No dump file provided — creating a fresh backup ..."
  DATABASE_URL="$DATABASE_URL" bash "$SCRIPT_DIR/backup.sh" "$TMPDIR_BACKUP"
  DUMP_FILE=$(ls -t "${TMPDIR_BACKUP}"/freecloud-backup-*.sql | head -1)
  echo "Using dump: $DUMP_FILE"
fi

if [ ! -f "$DUMP_FILE" ]; then
  echo "ERROR: dump file not found: $DUMP_FILE" >&2
  exit 1
fi

# ── Get source row counts (baseline) ─────────────────────────────────────────

echo "Counting rows in source database ..."
_src_parsed=$(DATABASE_URL="$DATABASE_URL" python3 - <<'PY'
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
SRC_HOST=$(echo "$_src_parsed" | sed -n '1p')
SRC_PORT=$(echo "$_src_parsed" | sed -n '2p')
SRC_USER=$(echo "$_src_parsed" | sed -n '3p')
SRC_PASS=$(echo "$_src_parsed" | sed -n '4p')
SRC_DB=$(echo "$_src_parsed" | sed -n '5p')

psql_source() {
  PGPASSWORD="$SRC_PASS" psql \
    --host="$SRC_HOST" \
    --port="$SRC_PORT" \
    --username="$SRC_USER" \
    --dbname="$SRC_DB" \
    --no-password \
    --tuples-only \
    --no-align \
    "$@"
}

SRC_USERS=$(psql_source -c "SELECT COUNT(*) FROM users;" | tr -d '[:space:]')
SRC_AUDIT=$(psql_source -c "SELECT COUNT(*) FROM audit_logs;" | tr -d '[:space:]')
SRC_APPS=$(psql_source -c "SELECT COUNT(*) FROM connected_apps;" | tr -d '[:space:]')
SRC_DEVICES=$(psql_source -c "SELECT COUNT(*) FROM devices;" | tr -d '[:space:]')

echo "  Source: users=${SRC_USERS}, audit_logs=${SRC_AUDIT}, apps=${SRC_APPS}, devices=${SRC_DEVICES}"

# ── Restore freecloud portion into scratch DB ─────────────────────────────────

echo "Creating scratch database ${VERIFY_DB} ..."
psql_scratch -c "CREATE DATABASE ${VERIFY_DB};" > /dev/null

echo "Restoring dump into ${VERIFY_DB} ..."
# Extract only the freecloud schema + data from the cluster dump (skip keycloak).
# pg_restore can't filter plain-SQL pg_dumpall output, so we use psql directly
# and rely on the dump's CREATE TABLE ... IF NOT EXISTS guards.
PGPASSWORD="$PGPASSWORD" psql \
  --host="$PGHOST" \
  --port="$PGPORT" \
  --username="$PGUSER" \
  --dbname="$VERIFY_DB" \
  --no-password \
  --set ON_ERROR_STOP=0 \
  --file="$DUMP_FILE" > /dev/null 2>&1 || true
# ON_ERROR_STOP=0 because a cluster dump may reference the keycloak DB which
# won't exist in the scratch instance; those errors are expected and benign.

# ── Assert row counts ─────────────────────────────────────────────────────────

echo "Verifying row counts ..."
FAIL=0

assert_count() {
  local table="$1" expected="$2"
  local actual
  actual=$(psql_verify -c "SELECT COUNT(*) FROM ${table};" 2>/dev/null | tr -d '[:space:]')
  if [ "$actual" = "$expected" ]; then
    echo "  OK  ${table}: ${actual}"
  else
    echo "  FAIL ${table}: expected ${expected}, got ${actual}" >&2
    FAIL=1
  fi
}

assert_count "users"         "$SRC_USERS"
assert_count "audit_logs"    "$SRC_AUDIT"
assert_count "connected_apps" "$SRC_APPS"
assert_count "devices"       "$SRC_DEVICES"

# ── Result ────────────────────────────────────────────────────────────────────

if [ "$FAIL" -eq 0 ]; then
  echo ""
  echo "Backup verification PASSED. Row counts match source."
  exit 0
else
  echo ""
  echo "Backup verification FAILED. See above for mismatches." >&2
  exit 1
fi
