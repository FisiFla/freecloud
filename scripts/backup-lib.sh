#!/usr/bin/env bash
# backup-lib.sh — shared helpers for the FreeCloud backup/restore scripts.
#
# This file is meant to be SOURCED, not executed:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   # shellcheck source=backup-lib.sh
#   . "${SCRIPT_DIR}/backup-lib.sh"
#
# Helpers:
#   fc_valid_db_name NAME            allow-list ^[A-Za-z0-9_]+$ (exit 0/1)
#   fc_extract_db_names DUMP_FILE     print validated CREATE DATABASE names
#                                     (case-insensitive match, strips one pair
#                                     of surrounding quotes), aborting on the
#                                     first invalid name
#   fc_db_table_counts DUMP_FILE      print "<db> <create-table-count>" per
#                                     database section of a pg_dumpall dump
#   fc_mac_key_hex                    hex MAC key derived from
#                                     BACKUP_PASSPHRASE under a distinct label
#   fc_hmac_file FILE                 hex HMAC-SHA256 of FILE under that key
#   fc_verify_hmac FILE EXPECTED_HEX  compare, with a tamper message on mismatch
#   fc_check_secrets_tar TAR_FILE     reject tars with .., absolute paths,
#                                     non-.secrets members or non-regular/dir
#                                     entries (symlink/hardlink/device/...)
#   fc_load_secrets_env ENV_FILE      export KEY=value lines literally
#                                     (KEY ^[A-Z_][A-Z0-9_]*$), aborting on any
#                                     other non-blank non-comment line; never
#                                     evaluates values
#
# Requires only: bash, openssl, python3, tar, sha256sum (all already required
# by the backup scripts).

# Guard against execution: this file defines functions for sourcing.
if [ "${BASH_SOURCE[0]:-}" = "$0" ]; then
  echo "ERROR: backup-lib.sh must be sourced, not executed." >&2
  exit 1
fi

# Encryption parameters for new bundles (backup-bundle.sh writes these,
# verify-bundle.sh enforces them). Consumed by sourcing scripts (unused here
# by design).
# shellcheck disable=SC2034
FC_PBKDF2_ITER=600000
FC_MAC_LABEL="FreeCloud-backup-mac-v1"
# shellcheck disable=SC2034
FC_ENCRYPTION_ID="aes-256-cbc-pbkdf2-600k-hmac-sha256-v1"

# fc_valid_db_name NAME — exit 0 iff NAME matches ^[A-Za-z0-9_]+$.
fc_valid_db_name() {
  case "${1:-}" in
    ''|*[!A-Za-z0-9_]*) return 1 ;;
    *) return 0 ;;
  esac
}

# fc_extract_db_names DUMP_FILE — print each database named by a
# CREATE DATABASE statement (matched case-insensitively: SQL keywords are
# case-insensitive, so a lowercase line executes just the same), one per
# line, sorted unique. Aborts (exit 1) on the first name outside the
# allow-list instead of returning a partial list.
fc_extract_db_names() {
  local dump="$1" raw name
  [ -f "${dump}" ] || { echo "ERROR: dump file not found: ${dump}" >&2; return 1; }
  local tmp
  tmp="$(mktemp)" || return 1
  # Extract the token after CREATE DATABASE case-insensitively; keep quoted
  # and unquoted spellings (a quoted name keeps its quotes for now).
  grep -i -o -E '^CREATE DATABASE ("[^"]*"|[^ ]+)' "${dump}" \
    | sed -E 's/^[^ ]+ [^ ]+ //' > "${tmp}" || true
  raw="$(sort -u "${tmp}")"
  rm -f "${tmp}"
  [ -n "${raw}" ] || return 0
  while IFS= read -r name; do
    [ -n "${name}" ] || continue
    # Trailing semicolons/whitespace are statement punctuation, not name —
    # strip FIRST (the extractor keeps them, e.g. `"mydb";`).
    name="$(printf '%s' "${name}" | sed 's/[;[:space:]]*$//')"
    # A fully double-quoted identifier is legitimate ("mydb"); strip exactly
    # one surrounding pair. Anything quoted-but-malformed fails below.
    case "${name}" in
      '"'*'"') name="${name#\"}"; name="${name%\"}" ;;
    esac
    if ! fc_valid_db_name "${name}"; then
      echo "ERROR: invalid database name in dump ${dump}: '${name}'" >&2
      echo "       Refusing to run: database names from a dump must match ^[A-Za-z0-9_]+\$." >&2
      return 1
    fi
    printf '%s\n' "${name}"
  done <<< "${raw}"
}

# fc_db_table_counts DUMP_FILE — print "<db> <count>" lines (sorted by db)
# counting "^CREATE TABLE " statements per \connect section of a pg_dumpall
# dump, unioned with every CREATE DATABASE name (0 when the dump carries no
# section for it). Both backup-bundle.sh (manifest inventory) and
# verify-bundle.sh (recompute check) use this so the two cannot drift.
fc_db_table_counts() {
  local dump="$1"
  [ -f "${dump}" ] || { echo "ERROR: dump file not found: ${dump}" >&2; return 1; }
  local dbs counts_tmp
  dbs="$(fc_extract_db_names "${dump}")" || return 1
  counts_tmp="$(mktemp)" || return 1
  awk '
    BEGIN { db = "" }
    # A \connect line opens exactly one database section (psql meta-commands
    # are lowercase; the keyword match for CREATE TABLE below is uppercase
    # because that is what pg_dumpall emits, matched case-insensitively to
    # be safe against hand-built dumps).
    /^\\connect[ \t]/ {
      db = $2
      gsub(/"/, "", db)
      gsub(/;$/, "", db)
      seen_db[db] = 1
      next
    }
    { upper = toupper($0) }
    upper ~ /^CREATE TABLE / {
      if (db != "") count[db]++
      next
    }
    END {
      for (d in seen_db) {
        printf "%s %d\n", d, count[d] + 0
      }
    }
  ' "${dump}" > "${counts_tmp}" || { rm -f "${counts_tmp}"; return 1; }
  {
    cat "${counts_tmp}"
    # Databases named by CREATE DATABASE but without a section count 0.
    while IFS= read -r name; do
      [ -n "${name}" ] || continue
      if ! grep -q "^${name} " "${counts_tmp}"; then
        printf '%s 0\n' "${name}"
      fi
    done <<< "${dbs}"
  } | sort -u
  rm -f "${counts_tmp}"
}

# fc_mac_key_hex — derive the 256-bit MAC key from BACKUP_PASSPHRASE under a
# distinct domain label (so the MAC key can never equal an encryption key
# even if openssl's KDF were fed the same passphrase).
fc_mac_key_hex() {
  if [ -z "${BACKUP_PASSPHRASE:-}" ]; then
    echo "ERROR: BACKUP_PASSPHRASE is not set (MAC key derivation needs it)." >&2
    return 1
  fi
  printf '%s' "${FC_MAC_LABEL}:${BACKUP_PASSPHRASE}" | sha256sum | awk '{print $1}'
}

# fc_hmac_file FILE — print the hex HMAC-SHA256 of FILE under the derived key.
fc_hmac_file() {
  local file="$1" key hex
  [ -f "${file}" ] || { echo "ERROR: file not found for HMAC: ${file}" >&2; return 1; }
  key="$(fc_mac_key_hex)" || return 1
  hex="$(openssl dgst -sha256 -mac HMAC -macopt hexkey:"${key}" "${file}" 2>/dev/null | awk '{print $NF}')"
  [ -n "${hex}" ] || { echo "ERROR: openssl HMAC computation failed." >&2; return 1; }
  printf '%s\n' "${hex}"
  # Best effort: do not leave the key in the environment of children.
  key=""
}

# fc_verify_hmac FILE EXPECTED_HEX — exit 0 iff the file's HMAC matches.
fc_verify_hmac() {
  local file="$1" expected="$2" got
  got="$(fc_hmac_file "${file}")" || return 1
  if [ "${got}" = "${expected}" ]; then
    return 0
  fi
  echo "ERROR: HMAC verification FAILED for ${file}." >&2
  echo "       The file was tampered with or the wrong BACKUP_PASSPHRASE was used." >&2
  echo "       Refusing to proceed." >&2
  return 1
}

# fc_check_secrets_tar TAR_FILE — exit 0 iff every member of the tar is a
# regular file or directory under the .secrets/ prefix, with no "..", no
# absolute path and no symlink/hardlink/device/fifo entry. Anything else is
# rejected: extracting it with plain `tar -xf` would write outside the
# checkout or plant a symlink for a later privileged step to follow.
fc_check_secrets_tar() {
  local tarfile="$1"
  [ -f "${tarfile}" ] || { echo "ERROR: tar file not found: ${tarfile}" >&2; return 1; }
  TARFILE="${tarfile}" python3 - <<'PY'
import os, sys, tarfile

path = os.environ["TARFILE"]
try:
    t = tarfile.open(path, "r")
except Exception as e:
    print(f"ERROR: cannot read {path} as a tar archive: {e}", file=sys.stderr)
    sys.exit(1)

bad = None
with t:
    for m in t.getmembers():
        name = m.name
        if name in ("", ".", "./"):
            bad = f"empty/top-level member {name!r}"
            break
        if os.path.isabs(name):
            bad = f"absolute path member {name!r}"
            break
        # Directory members may carry a trailing slash (".secrets/"); strip
        # it before the component check so real bundles are accepted.
        stripped = name.rstrip("/")
        if any(p in ("", ".", "..") for p in stripped.split("/")):
            bad = f"path traversal member {name!r}"
            break
        norm = os.path.normpath(stripped)
        if norm != ".secrets" and not norm.startswith(".secrets/"):
            bad = f"member outside .secrets/ prefix: {name!r}"
            break
        if m.issym() or m.islnk():
            bad = f"link member {name!r} -> {m.linkname!r}"
            break
        if not (m.isreg() or m.isdir()):
            bad = f"non-regular member {name!r} (type {m.type!r})"
            break

if bad is not None:
    print(f"ERROR: unsafe secrets.tar member: {bad}.", file=sys.stderr)
    print("       Refusing to extract: every member must be a regular file or",
          file=sys.stderr)
    print("       directory under .secrets/ with no '..', no absolute path and no links.",
          file=sys.stderr)
    sys.exit(1)
print("secrets.tar member check: OK (regular files/dirs under .secrets/ only)")
PY
}

# fc_load_secrets_env ENV_FILE — export every KEY=value line literally.
# Blank lines and #-comments are skipped. Every other line must match
# ^[A-Z_][A-Z0-9_]*=.*$ or the load aborts (exit 1) without exporting
# anything further. Values are assigned with `export "KEY=value"`, which the
# shell stores verbatim — no eval, no source, no command/arithmetic
# substitution, no globbing. Safe for the generated hex/base64 values and
# for hand-edited files alike.
fc_load_secrets_env() {
  local file="$1" line key val lineno
  [ -f "${file}" ] || { echo "ERROR: secrets env file not found: ${file}" >&2; return 1; }
  lineno=0
  while IFS= read -r line || [ -n "${line}" ]; do
    lineno=$((lineno + 1))
    case "${line}" in
      ''|'#'*) continue ;;
    esac
    case "${line}" in
      [A-Z_]*=*) ;;
      *)
        echo "ERROR: ${file}:${lineno}: refusing line (not KEY=value with an UPPER_SNAKE key): ${line}" >&2
        return 1
        ;;
    esac
    key="${line%%=*}"
    val="${line#*=}"
    case "${key}" in
      ''|*[!A-Z0-9_]*)
        echo "ERROR: ${file}:${lineno}: refusing line with invalid key '${key}'." >&2
        return 1
        ;;
    esac
    export "${key}=${val}"
  done < "${file}"
}
