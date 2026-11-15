#!/usr/bin/env bash
# backup-bundle.sh — full FreeCloud backup bundle: PostgreSQL cluster dump
# PLUS the .secrets/ directory (and .env.prod when present), with a manifest.
#
# A database dump alone is NOT a working restore: encrypted provisioning
# credentials in the DB are useless without PROVISIONING_MASTER_KEY, sessions
# need AUTH_SECRET, etc. — all of which live in .secrets/. This script bundles
# them together so one artifact restores a working stack.
#
# Usage:
#   DATABASE_URL=postgres://user:pass@host:5432/freecloud \
#     BACKUP_PASSPHRASE=... scripts/backup-bundle.sh [OUTPUT_DIR]
#   BACKUP_PASSPHRASE=... scripts/backup-bundle.sh --compose [OUTPUT_DIR]
#
# OUTPUT_DIR defaults to ./backups. The bundle is published as a single
# encrypted .tar.gz.enc (+ .manifest.txt + .hmac sidecars), or — only with
# the explicit --allow-plaintext opt-out — as
# OUTPUT_DIR/freecloud-bundle-<UTC timestamp>/ (mode 0700, files 0600).
#
# Modes:
#   (default)  Requires DATABASE_URL; reuses scripts/backup.sh (pg_dumpall).
#   --compose  Dumps via `docker compose -f docker/docker-compose.prod.yml
#              exec postgres pg_dumpall` (production stack must be up).
#
# Environment:
#   BACKUP_PASSPHRASE     REQUIRED unless --allow-plaintext is passed. The
#                         bundle is encrypted with
#                         `openssl enc -aes-256-cbc -salt -pbkdf2 -iter 600000`
#                         and authenticated with HMAC-SHA256 over the
#                         ciphertext (encrypt-then-MAC, key derived from the
#                         passphrase under a distinct label), verified by
#                         scripts/verify-bundle.sh BEFORE decryption.
#                         Read it from a root-only file (the shipped systemd
#                         unit uses EnvironmentFile); never put it on a
#                         command line.
#   BACKUP_RETENTION_DAYS Prune bundles older than this many days after a
#                         successful run (default 14; 0 disables).
#   FREECLOUD_VERSION     Recorded in the manifest when set.
#
# Like backup.sh, everything is built in a staging directory and only renamed
# into place after validation — a failed run never leaves a file that looks
# like a good backup.

set -euo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
# shellcheck source=scripts/backup-lib.sh
. "${SCRIPT_DIR}/backup-lib.sh"

MODE="direct"
OUTPUT_DIR=""
ALLOW_PLAINTEXT=0
while [ $# -gt 0 ]; do
  case "$1" in
    --compose) MODE="compose" ;;
    --allow-plaintext) ALLOW_PLAINTEXT=1 ;;
    -h|--help)
      echo "Usage: scripts/backup-bundle.sh [--compose] [--allow-plaintext] [OUTPUT_DIR]"
      exit 0
      ;;
    --*)
      echo "ERROR: unknown option: $1" >&2
      echo "Usage: scripts/backup-bundle.sh [--compose] [--allow-plaintext] [OUTPUT_DIR]" >&2
      exit 1
      ;;
    *)
      if [ -n "${OUTPUT_DIR}" ]; then
        echo "ERROR: unexpected extra argument: $1" >&2
        exit 1
      fi
      OUTPUT_DIR="$1"
      ;;
  esac
  shift
done
OUTPUT_DIR="${OUTPUT_DIR:-./backups}"

# ── Preconditions ─────────────────────────────────────────────────────────────

if [ ! -d "${REPO_ROOT}/.secrets" ]; then
  echo "ERROR: ${REPO_ROOT}/.secrets does not exist." >&2
  echo "       A bundle without .secrets cannot restore a working stack" >&2
  echo "       (PROVISIONING_MASTER_KEY/AUTH_SECRET live there). Run" >&2
  echo "       'make prod-secrets-init' first, or run from the real checkout." >&2
  exit 1
fi

if [ "${MODE}" = "direct" ] && [ -z "${DATABASE_URL:-}" ]; then
  echo "ERROR: DATABASE_URL is not set (required unless --compose is used)." >&2
  exit 1
fi

# Fail closed: the bundle contains the full cluster dump plus every
# production secret, so an unencrypted bundle is only written with the
# explicit --allow-plaintext opt-out (drills/tests), never by accident.
ENCRYPT=0
if [ -n "${BACKUP_PASSPHRASE:-}" ]; then
  ENCRYPT=1
  if ! command -v openssl > /dev/null 2>&1; then
    echo "ERROR: BACKUP_PASSPHRASE is set but openssl is not on PATH." >&2
    exit 1
  fi
elif [ "${ALLOW_PLAINTEXT}" -eq 1 ]; then
  echo "WARNING: writing an UNENCRYPTED bundle (--allow-plaintext)." >&2
  echo "WARNING: it contains the full database dump and every production secret —" >&2
  echo "WARNING: protect it accordingly and prefer the encrypted path." >&2
else
  echo "ERROR: refusing to write an unencrypted bundle." >&2
  echo "       The bundle contains the full pg_dumpall plus every production" >&2
  echo "       secret; set BACKUP_PASSPHRASE (preferred — see docs/BACKUP_RESTORE.md" >&2
  echo "       for the root-only passphrase file) or pass --allow-plaintext to" >&2
  echo "       confirm you really want plaintext." >&2
  exit 1
fi

TIMESTAMP=$(date -u +"%Y%m%dT%H%M%SZ")
BUNDLE_NAME="freecloud-bundle-${TIMESTAMP}"
FINAL_DIR="${OUTPUT_DIR}/${BUNDLE_NAME}"
FINAL_ENC="${FINAL_DIR}.tar.gz.enc"
FINAL_MANIFEST="${OUTPUT_DIR}/${BUNDLE_NAME}.manifest.txt"
FINAL_HMAC="${FINAL_ENC}.hmac"
ENC_TMP="${FINAL_ENC}.tmp"
HMAC_TMP="${FINAL_HMAC}.tmp"

if [ -e "${FINAL_DIR}" ] || [ -e "${FINAL_ENC}" ]; then
  echo "ERROR: a bundle for ${TIMESTAMP} already exists in ${OUTPUT_DIR}." >&2
  echo "       Wait a second and retry." >&2
  exit 1
fi

mkdir -p "${OUTPUT_DIR}"
STAGING=$(mktemp -d "${OUTPUT_DIR}/.freecloud-bundle-staging.XXXXXX")
MOVED=0
SUCCESS=0

# Invoked through the EXIT trap below.
# shellcheck disable=SC2329
cleanup() {
  if [ "${SUCCESS}" -eq 1 ]; then
    return
  fi
  rm -rf "${STAGING}"
  rm -f "${ENC_TMP}" "${HMAC_TMP}"
  # An interrupted encryption must not leave the plaintext bundle behind.
  if [ "${MOVED}" -eq 1 ] && [ "${ENCRYPT}" -eq 1 ]; then
    rm -rf "${FINAL_DIR}"
    rm -f "${FINAL_MANIFEST}" "${FINAL_HMAC}"
  fi
}
trap cleanup EXIT

# ── Database dump ─────────────────────────────────────────────────────────────

if [ "${MODE}" = "direct" ]; then
  echo "Dumping PostgreSQL cluster via scripts/backup.sh ..."
  DATABASE_URL="${DATABASE_URL}" bash "${SCRIPT_DIR}/backup.sh" "${STAGING}"
  DUMP_COUNT=$(find "${STAGING}" -maxdepth 1 -name 'freecloud-backup-*.sql' | wc -l | tr -d '[:space:]')
  if [ "${DUMP_COUNT}" != "1" ]; then
    echo "ERROR: expected exactly one dump from backup.sh, found ${DUMP_COUNT}." >&2
    exit 1
  fi
  DUMP_FILE=$(find "${STAGING}" -maxdepth 1 -name 'freecloud-backup-*.sql' -print | head -1)
  mv "${DUMP_FILE}" "${STAGING}/cluster.sql"
else
  KC_DB=$(sed -n 's/^CREATE DATABASE \([A-Za-z0-9_]*\).*/\1/p' \
    "${REPO_ROOT}/docker/postgres/init-keycloak-db.sql" | head -1)
  if [ -z "${KC_DB}" ]; then
    echo "ERROR: could not parse the Keycloak database name from" >&2
    echo "       docker/postgres/init-keycloak-db.sql" >&2
    exit 1
  fi
  echo "Dumping PostgreSQL cluster via docker compose (postgres service) ..."
  if ! (cd "${REPO_ROOT}" && docker compose -f docker/docker-compose.prod.yml \
      exec -T postgres pg_dumpall -U freecloud --clean --if-exists) \
      > "${STAGING}/cluster.sql.tmp"; then
    echo "ERROR: pg_dumpall via docker compose failed — no bundle was written." >&2
    exit 1
  fi
  # Same sanity checks backup.sh applies: non-empty, and the dump must cover
  # both databases of the cluster.
  if [ ! -s "${STAGING}/cluster.sql.tmp" ]; then
    echo "ERROR: pg_dumpall produced an empty dump — no bundle was written." >&2
    exit 1
  fi
  for db in freecloud "${KC_DB}"; do
    if ! grep -qE "^CREATE DATABASE ${db}([[:space:]]|;)" "${STAGING}/cluster.sql.tmp"; then
      echo "ERROR: the dump does not contain the '${db}' database." >&2
      echo "       No bundle was written." >&2
      exit 1
    fi
  done
  mv "${STAGING}/cluster.sql.tmp" "${STAGING}/cluster.sql"
fi

# ── Secrets + config ──────────────────────────────────────────────────────────

echo "Archiving .secrets/ ..."
tar -cf "${STAGING}/secrets.tar" -C "${REPO_ROOT}" .secrets

ENV_NOTE="absent"
if [ -f "${REPO_ROOT}/.env.prod" ]; then
  cp "${REPO_ROOT}/.env.prod" "${STAGING}/.env.prod"
  ENV_NOTE="present"
fi

# ── Manifest (never any secret values) ────────────────────────────────────────

GIT_HEAD=$(git -C "${REPO_ROOT}" rev-parse HEAD 2> /dev/null || echo "unknown")
file_size() { wc -c < "$1" | tr -d '[:space:]'; }

ENCRYPTION_LABEL="none"
if [ "${ENCRYPT}" -eq 1 ]; then
  ENCRYPTION_LABEL="${FC_ENCRYPTION_ID}"
fi

{
  echo "FreeCloud backup bundle"
  echo "timestamp_utc: ${TIMESTAMP}"
  echo "mode: ${MODE}"
  echo "hostname: $(hostname)"
  echo "git_head: ${GIT_HEAD}"
  echo "freecloud_version: ${FREECLOUD_VERSION:-unset}"
  echo "encryption: ${ENCRYPTION_LABEL}"
  echo "env_prod: ${ENV_NOTE}"
  echo "files:"
  echo "  cluster.sql $(file_size "${STAGING}/cluster.sql") bytes"
  echo "  secrets.tar $(file_size "${STAGING}/secrets.tar") bytes"
  if [ "${ENV_NOTE}" = "present" ]; then
    echo "  .env.prod $(file_size "${STAGING}/.env.prod") bytes"
  fi
  echo "sha256:"
  if [ "${ENV_NOTE}" = "present" ]; then
    (cd "${STAGING}" && sha256sum cluster.sql secrets.tar .env.prod)
  else
    (cd "${STAGING}" && sha256sum cluster.sql secrets.tar)
  fi
  echo "inventory:"
  # Per-database CREATE TABLE counts parsed from the dump itself (cheap
  # object inventory). restore.sh compares these against the live databases
  # after a restore; any mismatch fails the restore. Aborts here when the
  # dump carries a database name outside the allow-list.
  fc_db_table_counts "${STAGING}/cluster.sql"
} > "${STAGING}/manifest.txt"

chmod 0600 "${STAGING}"/*
if [ -f "${STAGING}/.env.prod" ]; then
  chmod 0600 "${STAGING}/.env.prod"
fi
chmod 0700 "${STAGING}"

# ── Publish ───────────────────────────────────────────────────────────────────

mv "${STAGING}" "${FINAL_DIR}"
MOVED=1

if [ "${ENCRYPT}" -eq 1 ]; then
  echo "Encrypting bundle (aes-256-cbc, pbkdf2 ${FC_PBKDF2_ITER} iterations) ..."
  tar -czf - -C "${OUTPUT_DIR}" "${BUNDLE_NAME}" \
    | openssl enc -aes-256-cbc -salt -pbkdf2 -iter "${FC_PBKDF2_ITER}" \
        -pass env:BACKUP_PASSPHRASE -out "${ENC_TMP}"
  chmod 0600 "${ENC_TMP}"
  mv "${ENC_TMP}" "${FINAL_ENC}"
  cp "${FINAL_DIR}/manifest.txt" "${FINAL_MANIFEST}"
  chmod 0600 "${FINAL_MANIFEST}"
  # Encrypt-then-MAC: HMAC-SHA256 over the ciphertext AND over the outer
  # manifest, under the passphrase-derived key. verify-bundle.sh checks both
  # BEFORE decrypting anything.
  {
    printf 'ciphertext %s\n' "$(fc_hmac_file "${FINAL_ENC}")"
    printf 'manifest %s\n' "$(fc_hmac_file "${FINAL_MANIFEST}")"
  } > "${HMAC_TMP}"
  chmod 0600 "${HMAC_TMP}"
  mv "${HMAC_TMP}" "${FINAL_HMAC}"
  rm -rf "${FINAL_DIR}"
  SUCCESS=1
  trap - EXIT
  echo "Encrypted bundle complete: ${FINAL_ENC}"
  echo "Manifest: ${FINAL_MANIFEST}"
  echo "MAC sidecar: ${FINAL_HMAC}"
else
  SUCCESS=1
  trap - EXIT
  echo "Bundle complete: ${FINAL_DIR}"
fi

# ── Retention ─────────────────────────────────────────────────────────────────

RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-14}"
case "${RETENTION_DAYS}" in
  0) ;;
  ''|*[!0-9]*)
    echo "WARNING: BACKUP_RETENTION_DAYS='${RETENTION_DAYS}' is not a number; skipping prune." >&2
    ;;
  *)
    find "${OUTPUT_DIR}" -maxdepth 1 -name 'freecloud-bundle-*' \
      -mtime "+${RETENTION_DAYS}" -print0 \
      | while IFS= read -r -d '' entry; do
          echo "Pruning old bundle: ${entry}"
          rm -rf -- "${entry}" || echo "WARNING: could not prune ${entry}" >&2
        done
    ;;
esac

if [ "${ENCRYPT}" -eq 1 ]; then
  echo "Verify/decrypt (MACs are checked BEFORE decryption):"
  echo "  BACKUP_PASSPHRASE=<passphrase> scripts/verify-bundle.sh '${FINAL_ENC}' [output-dir]"
  echo "Bundles encrypted by older scripts (no .hmac sidecar) need: scripts/verify-bundle.sh --legacy ..."
fi
