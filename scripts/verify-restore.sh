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
# throwaway host. The script creates fresh databases named freecloud_verify
# and keycloak_verify, restores the matching portion of the dump into each,
# runs assertions, then drops them again.
#
# The dump is a pg_dumpall CLUSTER dump: it contains CREATE DATABASE and
# \connect statements. Only the sections belonging to the freecloud and
# Keycloak databases are restored — the cluster-level statements are dropped,
# so the scratch host is never left with stray databases named after the
# production ones. Both sections are verified: a dump whose Keycloak section
# is missing or truncated fails, even when the freecloud section is intact.
#
# Does NOT require Docker — any reachable PostgreSQL instance works.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMPDIR_BACKUP="$(mktemp -d)"
VERIFY_DB=""
KC_VERIFY_DB=""
# The Keycloak database name is defined by the postgres init script.
KC_DB=$(sed -n 's/^CREATE DATABASE \([A-Za-z0-9_]*\).*/\1/p' \
  "${SCRIPT_DIR}/../docker/postgres/init-keycloak-db.sql" 2> /dev/null | head -1)
KC_DB="${KC_DB:-keycloak}"
SCRATCH_READY=0
PREEXISTING_DBS_FILE="$TMPDIR_BACKUP/preexisting-databases.txt"

# Invoked through the EXIT trap below.
# shellcheck disable=SC2329
cleanup() {
  if [ "${SCRATCH_READY}" -eq 1 ]; then
    if [ -n "${VERIFY_DB:-}" ]; then
      echo "Dropping scratch database ${VERIFY_DB} ..."
      psql_scratch -c "DROP DATABASE IF EXISTS \"${VERIFY_DB}\" WITH (FORCE);" > /dev/null 2>&1 || true
    fi
    if [ -n "${KC_VERIFY_DB:-}" ]; then
      echo "Dropping scratch database ${KC_VERIFY_DB} ..."
      psql_scratch -c "DROP DATABASE IF EXISTS \"${KC_VERIFY_DB}\" WITH (FORCE);" > /dev/null 2>&1 || true
    fi
    # Safety net: if a dump still carried cluster-level CREATE DATABASE
    # statements, databases named after the source would exist on the scratch
    # host. Drop every database that appeared while this script ran — only
    # databases that did not exist beforehand are touched.
    while IFS= read -r db; do
      [ -n "${db}" ] || continue
      if grep -Fxq "${db}" "${PREEXISTING_DBS_FILE}" 2>/dev/null; then
        continue
      fi
      if [ "${db}" = "${VERIFY_DB}" ] || [ "${db}" = "${KC_VERIFY_DB}" ]; then
        continue
      fi
      echo "Dropping stray database ${db} ..."
      psql_scratch -c "DROP DATABASE IF EXISTS \"${db}\" WITH (FORCE);" > /dev/null 2>&1 || true
    done <<< "$(psql_scratch -c "SELECT datname FROM pg_database WHERE datistemplate = false;" 2>/dev/null || true)"
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
KC_VERIFY_DB="keycloak_verify_$$"

psql_scratch() {
  psql \
    --host="$PGHOST" \
    --port="$PGPORT" \
    --username="$PGUSER" \
    --dbname="postgres" \
    --no-password \
    -X \
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
    -X \
    --tuples-only \
    --no-align \
    "$@"
}

psql_kc_verify() {
  psql \
    --host="$PGHOST" \
    --port="$PGPORT" \
    --username="$PGUSER" \
    --dbname="$KC_VERIFY_DB" \
    --no-password \
    -X \
    --tuples-only \
    --no-align \
    "$@"
}

# Remember which databases already exist on the scratch host so the cleanup
# can drop anything this run created — and nothing else.
psql_scratch -c "SELECT datname FROM pg_database WHERE datistemplate = false;" > "${PREEXISTING_DBS_FILE}"
SCRATCH_READY=1

# ── Dump (if not provided) ────────────────────────────────────────────────────

if [ -z "$DUMP_FILE" ]; then
  echo "No dump file provided — creating a fresh backup ..."
  DATABASE_URL="$DATABASE_URL" bash "$SCRIPT_DIR/backup.sh" "$TMPDIR_BACKUP"
  DUMP_FILE=$(find "${TMPDIR_BACKUP}" -maxdepth 1 -name 'freecloud-backup-*.sql' -print | head -1)
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
TARGET_DB="${SRC_DB}"

psql_source() {
  PGPASSWORD="$SRC_PASS" psql \
    --host="$SRC_HOST" \
    --port="$SRC_PORT" \
    --username="$SRC_USER" \
    --dbname="$SRC_DB" \
    --no-password \
    -X \
    --tuples-only \
    --no-align \
    "$@"
}

SRC_USERS=$(psql_source -c "SELECT COUNT(*) FROM users;" | tr -d '[:space:]')
SRC_AUDIT=$(psql_source -c "SELECT COUNT(*) FROM audit_logs;" | tr -d '[:space:]')
SRC_APPS=$(psql_source -c "SELECT COUNT(*) FROM connected_apps;" | tr -d '[:space:]')
SRC_DEVICES=$(psql_source -c "SELECT COUNT(*) FROM devices;" | tr -d '[:space:]')

echo "  Source: users=${SRC_USERS}, audit_logs=${SRC_AUDIT}, apps=${SRC_APPS}, devices=${SRC_DEVICES}"

# ── Extract the target section from the cluster dump ──────────────────────────
#
# pg_dumpall output is a cluster dump: it contains CREATE DATABASE statements
# and \connect commands. Feeding the whole file to `psql --dbname=$VERIFY_DB`
# creates databases named after the source (freecloud, keycloak) on the scratch
# host and leaves $VERIFY_DB empty — which is why the assertions below used to
# fail every single time. Extract only the section that belongs to the source
# database and drop the cluster-level statements.

extract_section() {
  local dump="$1" target="$2" out="$3"
  awk -v target="${target}" '
    # A \connect line starts the section of exactly one database.
    /^\\connect[ \t]/ {
      db = $2
      gsub(/"/, "", db)
      in_section = (db == target)
      seen = 1
      next
    }
    # Everything before the first \connect is the cluster-global section
    # (roles, role settings). It is skipped on purpose: the scratch host has
    # its own roles, and a restored password hash would break them.
    seen == 0 { next }
    # Cluster-level statements do not belong to any database section.
    /^CREATE DATABASE / { next }
    /^ALTER DATABASE /  { next }
    /^DROP DATABASE /   { next }
    in_section { print }
  ' "${dump}" > "${out}"
}

SECTION_FILE="$TMPDIR_BACKUP/target-section.sql"
extract_section "$DUMP_FILE" "$TARGET_DB" "$SECTION_FILE"

if [ ! -s "$SECTION_FILE" ]; then
  if grep -q "^CREATE DATABASE " "$DUMP_FILE"; then
    echo "ERROR: the dump has no '${TARGET_DB}' section — it does not contain the source database." >&2
    exit 1
  fi
  # A single-database dump (pg_dump, no CREATE DATABASE / \connect): use it
  # as-is.
  echo "The dump is a single-database dump — restoring it as-is."
  cp "$DUMP_FILE" "$SECTION_FILE"
fi

KC_SECTION_FILE="$TMPDIR_BACKUP/keycloak-section.sql"
extract_section "$DUMP_FILE" "$KC_DB" "$KC_SECTION_FILE"

if [ ! -s "$KC_SECTION_FILE" ]; then
  echo "ERROR: dump does not contain the ${KC_DB} database." >&2
  echo "       A cluster backup must cover the Keycloak database too — a" >&2
  echo "       dump whose ${KC_DB} section is missing or empty cannot" >&2
  echo "       restore a working stack." >&2
  exit 1
fi

# ── Restore the sections into the scratch DBs ─────────────────────────────────
#
# ON_ERROR_STOP=0: a section still references database roles that do not exist
# on the scratch host (the cluster-global section is skipped), so statements
# like "ALTER TABLE ... OWNER TO freecloud" fail. Those errors are benign —
# the data is restored either way, and the assertions below are what actually
# decide whether the backup is usable.

restore_section() {
  local db="$1" file="$2" log="$3"
  echo "Creating scratch database ${db} ..."
  psql_scratch -c "CREATE DATABASE ${db};" > /dev/null
  if ! PGPASSWORD="$PGPASSWORD" psql \
    --host="$PGHOST" \
    --port="$PGPORT" \
    --username="$PGUSER" \
    --dbname="$db" \
    --no-password \
    -X \
    --set ON_ERROR_STOP=0 \
    --file="$file" > "$log" 2>&1; then
    echo "ERROR: the restore into ${db} failed." >&2
    tail -n 30 "$log" >&2
    exit 1
  fi
}

RESTORE_LOG="$TMPDIR_BACKUP/restore.log"
echo "Restoring the '${TARGET_DB}' section into ${VERIFY_DB} ..."
restore_section "$VERIFY_DB" "$SECTION_FILE" "$RESTORE_LOG"

KC_RESTORE_LOG="$TMPDIR_BACKUP/restore-keycloak.log"
echo "Restoring the '${KC_DB}' section into ${KC_VERIFY_DB} ..."
restore_section "$KC_VERIFY_DB" "$KC_SECTION_FILE" "$KC_RESTORE_LOG"

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

# Keycloak: there is no source-count baseline over DATABASE_URL (it points at
# the freecloud database), so assert the restored Keycloak DB is a real
# Keycloak schema with at least one realm. A truncated Keycloak section
# either has no such tables or no realm row — both fail here.
echo "Verifying Keycloak database ..."
for kc_table in realm user_entity; do
  kc_exists=$(psql_kc_verify -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = '${kc_table}';" 2> /dev/null | tr -d '[:space:]' || true)
  if [ "${kc_exists:-0}" = "1" ]; then
    echo "  OK  ${KC_DB}.${kc_table} exists"
  else
    echo "  FAIL ${KC_DB}.${kc_table} is missing in the restored Keycloak database" >&2
    FAIL=1
  fi
done

KC_REALMS=$(psql_kc_verify -c "SELECT COUNT(*) FROM realm;" 2> /dev/null | tr -d '[:space:]' || true)
case "${KC_REALMS:-}" in
  ''|*[!0-9]*) KC_REALMS=0 ;;
esac
if [ "${KC_REALMS}" -ge 1 ]; then
  echo "  OK  ${KC_DB}.realm: ${KC_REALMS} (>= 1)"
else
  echo "  FAIL ${KC_DB}.realm: expected >= 1, got ${KC_REALMS}" >&2
  FAIL=1
fi

# ── Result ────────────────────────────────────────────────────────────────────

if [ "$FAIL" -eq 0 ]; then
  echo ""
  echo "Backup verification PASSED. Row counts match source; Keycloak realm present."
  exit 0
else
  echo "" >&2
  echo "Backup verification FAILED. See above for mismatches." >&2
  echo "Last 20 lines of the restore logs:" >&2
  tail -n 20 "$RESTORE_LOG" >&2
  tail -n 20 "$KC_RESTORE_LOG" >&2
  exit 1
fi
