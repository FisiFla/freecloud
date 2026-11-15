#!/usr/bin/env bash
# restore.sh — restore a FreeCloud PostgreSQL dump into a target database.
#
# Usage:
#   RESTORE_DATABASE_URL=postgres://user:pass@host:5432/postgres \
#     scripts/restore.sh --bundle <bundle-dir-or-.tar.gz.enc>
#   RESTORE_DATABASE_URL=postgres://user:pass@host:5432/postgres \
#     scripts/restore.sh <dump_file.sql>
#
# --bundle is preferred: the bundle is verified with scripts/verify-bundle.sh
# FIRST (integrity + manifest + secrets.tar member checks; encrypted bundles
# need BACKUP_PASSPHRASE and are authenticated before decryption), the
# cluster dump is restored, and the live table counts are compared against
# the manifest inventory. A bare dump file skips verification with a loud
# warning — only use it for dumps you trust.
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
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/backup-lib.sh
. "${SCRIPT_DIR}/backup-lib.sh"

BUNDLE=""
DUMP_FILE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --bundle)
      [ $# -ge 2 ] || { echo "ERROR: --bundle needs a value." >&2; exit 1; }
      BUNDLE="$2"
      shift 2
      continue
      ;;
    -h|--help)
      echo "Usage: RESTORE_DATABASE_URL=... scripts/restore.sh [--bundle <bundle>|<dump_file.sql>]" >&2
      exit 0
      ;;
    --*)
      echo "ERROR: unknown option: $1" >&2
      exit 1
      ;;
    *)
      if [ -n "${DUMP_FILE}" ]; then
        echo "ERROR: unexpected extra argument: $1" >&2
        exit 1
      fi
      DUMP_FILE="$1"
      ;;
  esac
  shift
done

MANIFEST=""
DECRYPT_TMP=""

if [ -n "${BUNDLE}" ]; then
  [ -n "${DUMP_FILE}" ] && { echo "ERROR: pass either --bundle or a dump file, not both." >&2; exit 1; }
  # Mandatory first step: verify the bundle before touching the target.
  if [ -d "${BUNDLE}" ]; then
    VERIFIED_DIR="$(bash "${SCRIPT_DIR}/verify-bundle.sh" "${BUNDLE}")"
  else
    case "${BUNDLE}" in
      *.tar.gz.enc) ;;
      *) echo "ERROR: --bundle needs a bundle directory or .tar.gz.enc file: ${BUNDLE}" >&2; exit 1 ;;
    esac
    [ -n "${BACKUP_PASSPHRASE:-}" ] || {
      echo "ERROR: BACKUP_PASSPHRASE is not set (required to verify/decrypt ${BUNDLE})." >&2
      exit 1
    }
    DECRYPT_TMP="$(mktemp -d -t freecloud-restore-decrypt.XXXXXX)"
    VERIFIED_DIR="$(bash "${SCRIPT_DIR}/verify-bundle.sh" "${BUNDLE}" "${DECRYPT_TMP}/bundle")"
  fi
  DUMP_FILE="${VERIFIED_DIR}/cluster.sql"
  MANIFEST="${VERIFIED_DIR}/manifest.txt"
  echo "Bundle verified: ${VERIFIED_DIR}"
else
  if [ -z "${DUMP_FILE}" ]; then
    echo "ERROR: provide a bundle (--bundle <bundle>) or a dump file." >&2
    echo "  Usage: RESTORE_DATABASE_URL=... scripts/restore.sh [--bundle <bundle>|freecloud-backup-YYYYMMDD.sql]" >&2
    exit 1
  fi
  echo "WARNING: restoring a bare dump file — bundle integrity was NOT verified." >&2
  echo "WARNING: prefer scripts/restore.sh --bundle <bundle> so the manifest," >&2
  echo "WARNING: hashes and secrets.tar members are checked before the restore." >&2
fi

if [ ! -f "${DUMP_FILE}" ]; then
  echo "ERROR: dump file not found: ${DUMP_FILE}" >&2
  exit 1
fi

if [ -z "${RESTORE_DATABASE_URL:-}" ]; then
  echo "ERROR: RESTORE_DATABASE_URL is not set." >&2
  echo "  Point it at the postgres maintenance database of your restore target:" >&2
  echo "  RESTORE_DATABASE_URL=postgres://user:pass@host:5432/postgres scripts/restore.sh --bundle <bundle>" >&2
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

# psql_db DBNAME ... — same, but connected to DBNAME (used for the
# per-database inventory counts; DBNAME is always allow-list validated).
psql_db() {
  local db="$1"
  shift
  psql \
    --host="$PGHOST" \
    --port="$PGPORT" \
    --username="$PGUSER" \
    --dbname="$db" \
    --no-password \
    -X \
    --tuples-only \
    --no-align \
    "$@"
}

# Validate every database name the dump would create BEFORE asking for
# confirmation: a tampered dump must be rejected before it is anywhere near
# the superuser connection (and its names are later interpolated into SQL).
EXPECTED_DBS="$(fc_extract_db_names "${DUMP_FILE}")" \
  || { echo "Refusing to restore a dump with an invalid database name." >&2; exit 1; }

echo "Restoring ${DUMP_FILE} into ${PGHOST}:${PGPORT} (database: ${PGDB}) ..."
echo "WARNING: existing databases in the dump will be dropped and recreated."
# The `|| true` keeps `set -e` from exiting silently on EOF (closed stdin
# must still take the loud "Aborted." path below).
read -r -p "Type YES to continue: " CONFIRM || true
if [ "$CONFIRM" != "YES" ]; then
  echo "Aborted." >&2
  exit 1
fi

PSQL_LOG="$(mktemp -t freecloud-restore.XXXXXX)"
cleanup() {
  rm -f "${PSQL_LOG}"
  if [ -n "${DECRYPT_TMP}" ]; then
    rm -rf "${DECRYPT_TMP}"
  fi
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
# database the dump creates actually exists on the target now, and that the
# live table counts match the manifest inventory (when one exists).

if [ -z "${EXPECTED_DBS}" ]; then
  echo "WARNING: the dump contains no CREATE DATABASE statements — nothing to verify." >&2
fi

FAILED=0
for db in ${EXPECTED_DBS}; do
  # $db is allow-list validated above, so this interpolation is safe.
  count=$(psql_target -c "SELECT count(*) FROM pg_database WHERE datname = '${db}';" | tr -d '[:space:]')
  if [ "${count}" = "1" ]; then
    echo "  OK  database ${db} exists"
  else
    echo "  FAIL database ${db} is missing after the restore" >&2
    FAILED=1
  fi
done

# ── Inventory comparison ─────────────────────────────────────────────────────
# ON_ERROR_STOP=0 swallows per-statement failures (a CREATE TABLE that died
# mid-restore would otherwise go unnoticed), so compare the manifest's
# per-database table counts against the live databases. Any mismatch fails
# the restore.

if [ -n "${MANIFEST}" ] && [ -f "${MANIFEST}" ] && grep -q "^inventory:$" "${MANIFEST}"; then
  echo "Comparing live table counts against the manifest inventory ..."
  while IFS= read -r inv_line; do
    [ -n "${inv_line}" ] || continue
    inv_db="${inv_line%% *}"
    inv_expected="${inv_line##* }"
    if ! fc_valid_db_name "${inv_db}"; then
      echo "  FAIL manifest inventory carries an invalid database name: '${inv_db}'" >&2
      FAILED=1
      continue
    fi
    case "${inv_expected}" in
      ''|*[!0-9]*)
        echo "  FAIL manifest inventory carries a non-numeric count: '${inv_line}'" >&2
        FAILED=1
        continue
        ;;
    esac
    # Only count databases that exist (a missing one already failed above).
    inv_in_dump=0
    for db in ${EXPECTED_DBS}; do
      if [ "${db}" = "${inv_db}" ]; then
        inv_in_dump=1
      fi
    done
    if [ "${inv_in_dump}" -eq 0 ]; then
      echo "  note: inventory entry '${inv_db}' is not in this dump — skipping" >&2
      continue
    fi
    inv_actual="$(psql_db "${inv_db}" -c "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog', 'information_schema') AND table_type = 'BASE TABLE';" 2>/dev/null | tr -d '[:space:]' || true)"
    case "${inv_actual}" in
      ''|*[!0-9]*)
        echo "  FAIL could not count tables in restored database ${inv_db}" >&2
        FAILED=1
        continue
        ;;
    esac
    if [ "${inv_actual}" = "${inv_expected}" ]; then
      echo "  OK  inventory ${inv_db}: ${inv_actual} tables (matches manifest)"
    else
      echo "  FAIL inventory mismatch for database ${inv_db}: manifest says ${inv_expected} tables, live database has ${inv_actual}" >&2
      FAILED=1
    fi
  done <<< "$(awk '/^inventory:$/{flag=1;next}flag' "${MANIFEST}" | grep -v '^$' || true)"
else
  echo "WARNING: no manifest inventory available for this restore —" >&2
  echo "WARNING: the databases exist, but completeness (table counts) was NOT proven." >&2
  echo "WARNING: bundles taken by current scripts always carry an inventory;" >&2
  echo "WARNING: treat this restore as unverified until the data is spot-checked." >&2
fi

if [ "${FAILED}" -ne 0 ]; then
  echo "" >&2
  echo "Restore finished with errors: the restored cluster is incomplete." >&2
  echo "See the psql output above for the failing statements." >&2
  exit 1
fi

echo "Restore complete."
