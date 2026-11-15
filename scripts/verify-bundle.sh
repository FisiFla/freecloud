#!/usr/bin/env bash
# verify-bundle.sh — the ONE integrity entry point for FreeCloud backup bundles.
#
# Verifies a bundle and, for encrypted bundles, decrypts it AFTER the
# authentication checks pass. The restore flow (scripts/restore.sh --bundle),
# the restore drill, the nightly job and the runbook in
# docs/BACKUP_RESTORE.md all run this as a mandatory first step; it aborts
# (non-zero exit) on any mismatch.
#
# Usage:
#   scripts/verify-bundle.sh [--legacy] <bundle> [output-dir]
#
#   <bundle>     a plaintext bundle directory, or a .tar.gz.enc file.
#   output-dir   (encrypted bundles only) directory to decrypt into; it must
#                not exist yet. Default: the .enc basename minus ".tar.gz.enc",
#                next to the .enc file.
#   --legacy     accept a bundle encrypted by the OLD format (openssl
#                aes-256-cbc with default PBKDF2 iterations and NO
#                authentication). Never used silently: the flag is required
#                and a loud warning is printed. Use it once to migrate old
#                bundles, then re-backup.
#
# For encrypted bundles BACKUP_PASSPHRASE must hold the passphrase (same
# variable the backup side uses; prefer the systemd EnvironmentFile over
# exporting it in a shell). Checks, in order:
#   1. the .hmac sidecar exists and both HMACs verify (ciphertext, then the
#      outer manifest) — NOTHING is decrypted before this passes;
#   2. decryption into output-dir;
#   3. outer manifest is byte-identical to the authenticated inner manifest;
#   4. sha256 of cluster.sql, secrets.tar and .env.prod (when recorded)
#      match the manifest;
#   5. secrets.tar members are safe (regular files/dirs under .secrets/);
#   6. the manifest inventory matches a recompute from cluster.sql.
# Plaintext bundles skip 1-3 (there is no passphrase key to MAC with, so at
# least the sha256 checks in 4 apply) and run 4-6 in place.
#
# Bundles written by older scripts may lack the .env.prod hash or the
# inventory section: those produce loud warnings, not silent passes.
#
# On success the verified plaintext bundle directory is printed to stdout
# (the ONLY stdout line; every log line goes to stderr) so callers can do:
#   BUNDLE_DIR="$(BACKUP_PASSPHRASE=... scripts/verify-bundle.sh file.enc dir)"

set -euo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/backup-lib.sh
. "${SCRIPT_DIR}/backup-lib.sh"

LEGACY=0
BUNDLE=""
OUT_DIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --legacy) LEGACY=1 ;;
    -h|--help)
      echo "Usage: scripts/verify-bundle.sh [--legacy] <bundle> [output-dir]"
      exit 0
      ;;
    --*)
      echo "ERROR: unknown option: $1" >&2
      echo "Usage: scripts/verify-bundle.sh [--legacy] <bundle> [output-dir]" >&2
      exit 1
      ;;
    *)
      if [ -z "${BUNDLE}" ]; then
        BUNDLE="$1"
      elif [ -z "${OUT_DIR}" ]; then
        OUT_DIR="$1"
      else
        echo "ERROR: unexpected extra argument: $1" >&2
        exit 1
      fi
      ;;
  esac
  shift
done

if [ -z "${BUNDLE}" ]; then
  echo "ERROR: provide the bundle directory or .tar.gz.enc file." >&2
  echo "Usage: scripts/verify-bundle.sh [--legacy] <bundle> [output-dir]" >&2
  exit 1
fi

log() { printf 'verify-bundle: %s\n' "$*" >&2; }
fail() { printf 'verify-bundle: ERROR: %s\n' "$*" >&2; exit 1; }
warn() { printf 'verify-bundle: WARNING: %s\n' "$*" >&2; }

# ── Plaintext-bundle verification (shared by all paths) ───────────────────────
# verify_plaintext_dir DIR — sha256 + tar-member + inventory checks in place.
verify_plaintext_dir() {
  local dir="$1" manifest="$1/manifest.txt"
  [ -f "${manifest}" ] || fail "${dir} has no manifest.txt — not a bundle."
  [ -f "${dir}/cluster.sql" ] || fail "${dir} has no cluster.sql."
  [ -f "${dir}/secrets.tar" ] || fail "${dir} has no secrets.tar."

  # 4. sha256 of every recorded file. Only the three known names are
  # honoured; anything else in the section is a tampered manifest.
  local hash_lines check_file
  hash_lines="$(awk '/^sha256:$/{flag=1;next}/^inventory:$/{flag=0}flag' "${manifest}")"
  [ -n "${hash_lines}" ] || fail "manifest has no sha256 section."
  check_file="$(mktemp)" || fail "could not create a temp file."
  while IFS= read -r hline; do
    [ -n "${hline}" ] || continue
    case "${hline}" in
      *"  cluster.sql"|*"  secrets.tar"|*"  .env.prod") ;;
      *)
        rm -f "${check_file}"
        fail "manifest sha256 section carries an unexpected line: ${hline}"
        ;;
    esac
    printf '%s\n' "${hline}" >> "${check_file}"
  done <<< "${hash_lines}"
  if ! (cd "${dir}" && sha256sum -c "${check_file}" >&2); then
    rm -f "${check_file}"
    fail "sha256 verification FAILED — the bundle was tampered with."
  fi
  rm -f "${check_file}"
  log "sha256 verification: OK"

  # Every bundle payload file must be covered by a recorded hash. Older
  # bundles predate the .env.prod hash: loud warning, not a silent pass.
  local env_note
  env_note="$(sed -n 's/^env_prod: //p' "${manifest}" | head -1)"
  if [ -f "${dir}/.env.prod" ]; then
    if grep -q "  .env.prod$" "${manifest}"; then
      : # covered by the sha256sum -c run above
    else
      warn "${dir}/.env.prod has NO recorded hash (bundle predates .env.prod hashing) — cannot verify it."
    fi
    if [ "${env_note}" = "absent" ]; then
      fail "manifest says env_prod: absent but .env.prod exists — inconsistent bundle."
    fi
  else
    if [ "${env_note}" = "present" ]; then
      fail "manifest says env_prod: present but .env.prod is missing."
    fi
  fi

  # Flag unexpected files (a plaintext bundle is not authenticated, so an
  # attacker can add entries; restores only consume the known names).
  local entry
  for entry in "${dir}"/* "${dir}"/.*; do
    case "${entry}" in
      "${dir}/."|"${dir}/..") continue ;;
    esac
    [ -e "${entry}" ] || continue
    case "$(basename "${entry}")" in
      cluster.sql|secrets.tar|.env.prod|manifest.txt) ;;
      *) warn "unexpected file in bundle directory (ignored): ${entry}" ;;
    esac
  done

  # 5. secrets.tar members must be safe BEFORE anything extracts it.
  fc_check_secrets_tar "${dir}/secrets.tar" >&2 \
    || fail "secrets.tar failed the member-safety check."

  # 6. Manifest inventory must match a recompute from cluster.sql.
  if ! grep -q "^inventory:$" "${manifest}"; then
    warn "manifest has NO inventory section (bundle predates table-count inventory) — skipping the inventory check."
    return 0
  fi
  local recorded recomputed inv_line inv_db inv_count
  recorded="$(awk '/^inventory:$/{flag=1;next}flag' "${manifest}" | grep -v '^$' || true)"
  [ -n "${recorded}" ] || fail "manifest has an empty inventory section."
  while IFS= read -r inv_line; do
    [ -n "${inv_line}" ] || continue
    inv_db="${inv_line%% *}"
    inv_count="${inv_line##* }"
    if ! fc_valid_db_name "${inv_db}"; then
      fail "manifest inventory carries an invalid database name: '${inv_db}'."
    fi
    case "${inv_count}" in
      ''|*[!0-9]*) fail "manifest inventory carries a non-numeric count: '${inv_line}'." ;;
    esac
  done <<< "${recorded}"
  recomputed="$(fc_db_table_counts "${dir}/cluster.sql")" \
    || fail "could not recompute table counts from cluster.sql."
  if [ "$(printf '%s\n' "${recorded}" | sort)" = "$(printf '%s\n' "${recomputed}" | sort)" ]; then
    log "inventory check: OK ($(printf '%s' "${recorded}" | wc -l | tr -d ' ') databases)"
  else
    fail "manifest inventory does NOT match cluster.sql (tampered manifest or dump)."
  fi
}

# ── Dispatch ──────────────────────────────────────────────────────────────────
if [ -d "${BUNDLE}" ]; then
  [ -n "${OUT_DIR}" ] && fail "output-dir only applies to encrypted (.tar.gz.enc) bundles."
  [ "${LEGACY}" -eq 0 ] || warn "--legacy has no effect on plaintext bundles (nothing to decrypt)."
  log "verifying plaintext bundle ${BUNDLE} ..."
  verify_plaintext_dir "${BUNDLE}"
  log "bundle verification: OK"
  printf '%s\n' "${BUNDLE}"
  exit 0
fi

[ -f "${BUNDLE}" ] || fail "bundle not found: ${BUNDLE} (need a bundle directory or .tar.gz.enc file)."
case "${BUNDLE}" in
  *.tar.gz.enc) ;;
  *) fail "${BUNDLE} is neither a bundle directory nor a .tar.gz.enc file." ;;
esac

if [ -z "${BACKUP_PASSPHRASE:-}" ]; then
  fail "BACKUP_PASSPHRASE is not set (required to verify/decrypt ${BUNDLE})."
fi
command -v openssl >/dev/null 2>&1 || fail "openssl is not on PATH."

# Sidecar layout written by backup-bundle.sh: for <name>.tar.gz.enc the MACs
# live in <name>.tar.gz.enc.hmac and the outer manifest in <name>.manifest.txt.
ENC_BASENAME="$(basename "${BUNDLE}" .tar.gz.enc)"
ENC_PARENT="$(dirname "${BUNDLE}")"
HMAC_FILE="${BUNDLE}.hmac"
OUTER_MANIFEST="${ENC_PARENT}/${ENC_BASENAME}.manifest.txt"
if [ -z "${OUT_DIR}" ]; then
  OUT_DIR="${ENC_PARENT}/${ENC_BASENAME}"
fi
[ -e "${OUT_DIR}" ] && fail "output directory already exists: ${OUT_DIR} (refusing to overwrite)."

if [ ! -f "${HMAC_FILE}" ]; then
  if [ "${LEGACY}" -eq 1 ]; then
    warn "LEGACY bundle: no .hmac sidecar — the ciphertext is UNAUTHENTICATED and will be decrypted without tamper evidence."
    warn "Migrate: re-take this backup with the current scripts after restoring."
  else
    fail "${BUNDLE} has no .hmac sidecar: refusing to decrypt an unauthenticated bundle. If this is a pre-MAC bundle, re-run with --legacy (see docs/BACKUP_RESTORE.md)."
  fi
else
  if [ "${LEGACY}" -eq 1 ]; then
    warn "--legacy given but a .hmac sidecar exists — verifying the MACs anyway (strictest path wins)."
  fi
  # 1. Authenticate BEFORE decrypting anything.
  [ -f "${OUTER_MANIFEST}" ] || fail "outer manifest missing: ${OUTER_MANIFEST}."
  cipher_hex="$(awk '$1=="ciphertext"{print $2}' "${HMAC_FILE}")"
  manifest_hex="$(awk '$1=="manifest"{print $2}' "${HMAC_FILE}")"
  case "${cipher_hex}" in
    ''|*[!0-9a-fA-F]*) fail "malformed .hmac sidecar (bad ciphertext entry)." ;;
  esac
  [ "${#cipher_hex}" -eq 64 ] || fail "malformed .hmac sidecar (bad ciphertext entry)."
  case "${manifest_hex}" in
    ''|*[!0-9a-fA-F]*) fail "malformed .hmac sidecar (bad manifest entry)." ;;
  esac
  [ "${#manifest_hex}" -eq 64 ] || fail "malformed .hmac sidecar (bad manifest entry)."
  log "verifying ciphertext HMAC (before decryption) ..."
  fc_verify_hmac "${BUNDLE}" "${cipher_hex}" >&2 \
    || fail "ciphertext HMAC mismatch — tampered bundle or wrong passphrase. NOTHING was decrypted."
  log "ciphertext HMAC: OK"
  log "verifying outer-manifest HMAC ..."
  fc_verify_hmac "${OUTER_MANIFEST}" "${manifest_hex}" >&2 \
    || fail "outer-manifest HMAC mismatch — tampered manifest. NOTHING was decrypted."
  log "outer-manifest HMAC: OK"
fi

# 2. Decrypt. New bundles use 600k PBKDF2 iterations; --legacy retries the old
# openssl defaults for pre-MAC bundles.
mkdir -p "${OUT_DIR}"
chmod 0700 "${OUT_DIR}"
if [ "${LEGACY}" -eq 1 ] && [ ! -f "${HMAC_FILE}" ]; then
  log "decrypting legacy bundle (old openssl defaults) ..."
  if ! openssl enc -d -aes-256-cbc -salt -pbkdf2 -pass env:BACKUP_PASSPHRASE \
      -in "${BUNDLE}" | tar -xzf - -C "${OUT_DIR}" --strip-components=1; then
    rm -rf "${OUT_DIR}"
    fail "legacy decryption failed (wrong passphrase or corrupt bundle)."
  fi
else
  log "decrypting (pbkdf2 ${FC_PBKDF2_ITER} iterations) ..."
  if ! openssl enc -d -aes-256-cbc -salt -pbkdf2 -iter "${FC_PBKDF2_ITER}" \
      -pass env:BACKUP_PASSPHRASE -in "${BUNDLE}" \
      | tar -xzf - -C "${OUT_DIR}" --strip-components=1; then
    rm -rf "${OUT_DIR}"
    fail "decryption failed (wrong passphrase or corrupt bundle)."
  fi
fi
chmod 0600 "${OUT_DIR}/"* 2>/dev/null || true
[ -f "${OUT_DIR}/.env.prod" ] && chmod 0600 "${OUT_DIR}/.env.prod"

# 3. The outer manifest must be the authenticated inner one.
if [ -f "${OUTER_MANIFEST}" ]; then
  if cmp -s "${OUTER_MANIFEST}" "${OUT_DIR}/manifest.txt"; then
    log "outer manifest matches the authenticated inner manifest."
  else
    warn "outer manifest differs from the decrypted inner manifest — trusting the authenticated inner copy."
  fi
fi

verify_plaintext_dir "${OUT_DIR}"
log "bundle verification: OK"
printf '%s\n' "${OUT_DIR}"
