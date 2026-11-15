#!/bin/sh
# C1 — First-boot secret generation.
#
# Writes .secrets/secrets.env with auto-generated values for secrets that
# cannot be self-managed by the Keycloak bootstrap engine (Epic A):
#
#   AUTH_SECRET          — Auth.js session-cookie signing key
#   POSTGRES_PASSWORD    — internal Postgres password
#   KC_ADMIN_PASSWORD    — first-boot Keycloak admin password
#   SCIM_BEARER_TOKEN    — inbound SCIM provisioning bearer token
#   SETUP_TOKEN          — one-time first-admin provisioning token (POST /api/v1/setup)
#   ACCESS_EVAL_TOKEN    — posture-evaluation token (backend ↔ Keycloak SPI)
#   FLEET_WEBHOOK_SECRET — Fleet enrollment-callback HMAC (+ default device-cookie HMAC)
#   PROVISIONING_MASTER_KEY — AES-256-GCM key for stored connector secrets
#   REDIS_PASSWORD — password for the production Redis (rate limiter store)
#
# It ALSO writes one file per secret to .secrets/files/<lowercase-key> so the
# production compose can mount them as Docker secrets (see docker-compose.prod.yml
# `secrets:` + the backend's <VAR>_FILE convention) instead of injecting the
# values as plain environment variables.
#
# DEVICE_COOKIE_SECRET is optional and intentionally NOT auto-generated: when
# unset, freecloud-device-id cookies are signed with FLEET_WEBHOOK_SECRET.
# Set DEVICE_COOKIE_SECRET in secrets.env by hand to rotate cookie signing
# independently of the Fleet webhook. Hand-added keys are preserved.
# It is mirrored one-per-file (empty when unset) so the prod backend can read
# it via DEVICE_COOKIE_SECRET_FILE; empty keeps meaning "not configured".
#
# SLACK_WEBHOOK_URL, LDAP_BIND_PASSWORD and GF_ADMIN_PASSWORD are likewise
# operator-managed and mirrored one-per-file (never generated): the backend
# reads the first two via <VAR>_FILE and Grafana reads its admin password
# via GF_SECURITY_ADMIN_PASSWORD__FILE, so none of them is interpolated
# by value into a container environment. See MIRRORED_KEYS below.
#
# Every line this script writes to secrets.env must match
#   ^[A-Z_][A-Z0-9_]*=[A-Za-z0-9_./+=:@-]*$
# because the file is a plain KEY=value list that older tooling shell-sourced
# (the Makefile now uses scripts/load-secrets-env.sh, which never evaluates
# it): anything outside that charset (spaces, quotes, $, backticks,
# command substitutions) would be interpreted by the shell instead of read
# as a literal value. Hand-added or hand-edited lines outside the charset
# abort the run with an error — pick a value from the allowed charset
# (a Slack webhook URL or API token passes as-is).
#
# SLACK_ALERT_WEBHOOK_URL is likewise operator-managed and never generated.
# Its per-key file (.secrets/files/slack_alert_webhook_url) is the Docker
# secret the observability overlay's Alertmanager reads via api_url_file, so
# this script mirrors the hand-added value (or an empty file when unset)
# into that file on every run that rewrites the per-key files. Empty is
# valid: Alertmanager starts and simply does not deliver to Slack.
# See docs/OBSERVABILITY.md "Slack alert routing".
#
# FLEET_API_TOKEN is likewise operator-managed (it is issued by the
# operator's FleetDM instance, so it cannot be generated): set it in
# .secrets/secrets.env and this script mirrors it to
# .secrets/files/fleet_api_token, the Docker secret the prod backend reads
# via FLEET_API_TOKEN_FILE. Unlike the Slack webhook, an unset token is NOT
# valid — prod Validate() refuses to start the backend without it, so a
# missing value fails fast with "FLEET_API_TOKEN must be set" instead of a
# confusing Fleet error at runtime.
#
# KEYCLOAK_CLIENT_SECRET is absent: Epic A's bootstrap engine self-manages it.
#
# Rotation semantics (docs/DEPLOYMENT.md "Rotating secrets"):
#   * Only MISSING keys are generated. Existing values are never rewritten, so
#     re-running this script never rotates a live secret by accident.
#   * To rotate a generated secret, remove just that key from
#     .secrets/secrets.env and re-run `make prod-secrets-init`.
#   * POSTGRES_PASSWORD and KC_ADMIN_PASSWORD are NOT rotated this way: the
#     Postgres image applies the password only on an empty data directory and
#     Keycloak stores the admin password in its own database after first boot.
#     Rotate them with ALTER ROLE / kcadm — see docs/DEPLOYMENT.md.

# Strongest options every shell that runs this supports. It executes as
# /bin/sh in the busybox:1.36 secrets-init container (busybox ash, which
# supports -u and -o pipefail) AND as host `sh` under `make verify-secrets`,
# where sh may be dash (no pipefail) — hence the guarded probe instead of a
# bare `set -o pipefail`, which would abort the run on dash. The explicit
# non-empty checks on every generated value below are the backstop where
# pipefail is unavailable: a failed generator pipeline can never silently
# produce an empty secret.
set -eu
set -o pipefail 2>/dev/null || true
# The secrets file (and every per-key file) must never be world-readable, not
# even for the window between creation and chmod.
umask 077

SECRETS_FILE=${SECRETS_FILE:-/run/freecloud/secrets.env}
SECRETS_DIR=${SECRETS_DIR:-/run/freecloud/files}

# Keys owned by the generator. Anything else in secrets.env is operator-managed
# and is copied through untouched.
GENERATED_KEYS="POSTGRES_PASSWORD KC_ADMIN_PASSWORD AUTH_SECRET SCIM_BEARER_TOKEN SETUP_TOKEN ACCESS_EVAL_TOKEN FLEET_WEBHOOK_SECRET PROVISIONING_MASTER_KEY REDIS_PASSWORD"

# Operator-managed keys mirrored one-per-file (like the generated ones) so
# they can be mounted as Docker secrets. Unlike GENERATED_KEYS these are
# never created here: an unset key mirrors as an empty file.
#
#   SLACK_ALERT_WEBHOOK_URL — Alertmanager api_url_file (empty = no delivery)
#   FLEET_API_TOKEN         — backend FLEET_API_TOKEN_FILE (required by backend)
#   SLACK_WEBHOOK_URL       — backend SLACK_WEBHOOK_URL_FILE (optional)
#   DEVICE_COOKIE_SECRET    — backend DEVICE_COOKIE_SECRET_FILE (optional)
#   LDAP_BIND_PASSWORD      — backend LDAP_BIND_PASSWORD_FILE (optional)
#   PROVISIONING_MASTER_KEY_PREVIOUS — backend ..._PREVIOUS_FILE (optional,
#                             retired key kept decrypt-only during a rotation)
#   GF_ADMIN_PASSWORD       — Grafana GF_SECURITY_ADMIN_PASSWORD__FILE
#                             (observability overlay; Grafana refuses to
#                             start when the file is empty — see its compose
#                             entrypoint guard and docs/OBSERVABILITY.md)
MIRRORED_KEYS="SLACK_ALERT_WEBHOOK_URL FLEET_API_TOKEN SLACK_WEBHOOK_URL DEVICE_COOKIE_SECRET LDAP_BIND_PASSWORD GF_ADMIN_PASSWORD PROVISIONING_MASTER_KEY_PREVIOUS"

gen() {
  # 32 random bytes → 64-char hex string (256 bits of entropy).
  head -c 32 /dev/urandom | od -A n -t x1 | tr -d ' \n'
}

gen_b64() {
  # 32 random bytes → base64 string for AES-256 keys.
  head -c 32 /dev/urandom | base64 | tr -d '\n'
}

# value_of KEY — print the current value of KEY in the secrets file.
# An absent key and a key set to an empty value both print nothing: an empty
# value is NOT a usable secret, so it counts as missing.
value_of() {
  [ -f "${SECRETS_FILE}" ] || return 0
  sed -n "s/^$1=//p" "${SECRETS_FILE}" | head -n 1
}

lower_key() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

# check_line LINE — require the safe secrets.env charset (see the header).
# Operators may still shell-source this file by hand, so a line
# outside ^[A-Z_][A-Z0-9_]*=[A-Za-z0-9_./+=:@-]*$ would be interpreted by the
# shell instead of read literally. Reject it with a clear error and abort
# BEFORE the atomic rename, so the previous secrets.env stays intact.
check_line() {
  case "$1" in
    *=*) ;;
    *)
      echo "[secrets-init] ERROR: refusing to write line without KEY=value form: '$1'" >&2
      exit 1
      ;;
  esac
  _key=${1%%=*}
  _value=${1#*=}
  case "${_key}" in
    ''|*[!A-Z0-9_]*|[!A-Z_]*)
      echo "[secrets-init] ERROR: refusing line with invalid key '${_key}': keys must match ^[A-Z_][A-Z0-9_]*\$" >&2
      exit 1
      ;;
  esac
  case "${_value}" in
    *[!A-Za-z0-9_./+=:@-]*)
      echo "[secrets-init] ERROR: refusing ${_key}: value contains characters outside [A-Za-z0-9_./+=:@-]." >&2
      echo "[secrets-init] ERROR: secrets.env is shell-sourced, so spaces, quotes, \$, backticks and other" >&2
      echo "[secrets-init] ERROR: shell metacharacters are not allowed — pick a value from the allowed charset" >&2
      echo "[secrets-init] ERROR: (letters, digits and _ . / + = : @ -; a Slack webhook URL or API token passes as-is)." >&2
      exit 1
      ;;
  esac
}

# ── Idempotency check ────────────────────────────────────────────────────────

missing=""
for key in ${GENERATED_KEYS}; do
  if [ -z "$(value_of "${key}")" ]; then
    missing="${missing} ${key}"
  fi
done

# The per-key files must exist too — an older deployment only has secrets.env.
files_ok=1
for key in ${GENERATED_KEYS}; do
  if [ ! -s "${SECRETS_DIR}/$(lower_key "${key}")" ]; then
    files_ok=0
  fi
done

# The mirrored operator-managed files must exist and match the hand-added
# secrets.env values, so a missing or stale file also counts as incomplete
# (this is what makes rotation work: edit secrets.env, re-run).
for key in ${MIRRORED_KEYS}; do
  mirror_file="${SECRETS_DIR}/$(lower_key "${key}")"
  mirror_current=""
  [ -f "${mirror_file}" ] && mirror_current=$(cat "${mirror_file}")
  if [ ! -f "${mirror_file}" ] || [ "${mirror_current}" != "$(value_of "${key}")" ]; then
    files_ok=0
  fi
done

# Validate the existing file even when nothing needs regenerating: a
# hand-added line lands in secrets.env between runs, so the early exit below
# must not vouch for a file it never checked — every secrets-init run
# guarantees the file is safe to shell-source.
if [ -f "${SECRETS_FILE}" ]; then
  while IFS= read -r existing_line; do
    case "${existing_line}" in
      ''|'#'*) continue ;;
      *=*) ;;
      *) continue ;;
    esac
    check_line "${existing_line}"
  done < "${SECRETS_FILE}"
fi

if [ -z "${missing}" ] && [ "${files_ok}" -eq 1 ]; then
  # Re-assert the 0444 mode written below: the non-root service users (e.g.
  # redis, uid 999) read the mounted secret as "other", and a .secrets/
  # restored from a backup under umask 077 comes back 0400, which would keep
  # those services from starting.
  for key in ${GENERATED_KEYS} ${MIRRORED_KEYS}; do
    chmod 0444 "${SECRETS_DIR}/$(lower_key "${key}")"
  done
  echo "[secrets-init] secrets.env already complete — skipping generation"
  exit 0
fi

if [ -n "${missing}" ]; then
  echo "[secrets-init] generating missing secret(s):${missing}"
fi

# ── Generate missing values, keep existing ones ─────────────────────────────

TMP_FILE="${SECRETS_FILE}.tmp.$$"
cleanup() {
  rm -f "${TMP_FILE}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

mkdir -p "$(dirname "${SECRETS_FILE}")" "${SECRETS_DIR}"
# The per-key files are mounted into containers that may run as a non-root uid,
# so they must be world-readable; the 0700 parent directory (.secrets) is what
# keeps them off the host's other users. A failed chmod aborts the run (no
# `|| true`): silently leaving the directory group/world-readable would
# defeat the umask above, and there is no bind-mount setup in the documented
# flows (make prod-secrets-init, the secrets-init compose service) where the
# generator cannot chmod the directory it just created.
chmod 700 "$(dirname "${SECRETS_FILE}")"

{
  echo "# Auto-generated by docker/init-secrets/generate.sh on first boot."
  echo "# DO NOT EDIT BY HAND. Remove a single key to rotate just that secret;"
  echo "# see docs/DEPLOYMENT.md \"Rotating secrets\"."
} > "${TMP_FILE}"

for key in ${GENERATED_KEYS}; do
  value=$(value_of "${key}")
  if [ -z "${value}" ]; then
    case "${key}" in
      PROVISIONING_MASTER_KEY) value=$(gen_b64) ;;
      *) value=$(gen) ;;
    esac
    # An empty generator result (failed /dev/urandom, od or base64 — note
    # `set -e` does not fire inside $() on every shell) must abort instead
    # of being written as an empty secret.
    if [ -z "${value}" ]; then
      echo "[secrets-init] ERROR: generator produced an empty value for ${key}; aborting instead of writing an empty secret" >&2
      exit 1
    fi
    echo "[secrets-init] generated ${key}"
  fi
  # Validate preserved values too: a hand-edited generated key (e.g. a
  # rotated POSTGRES_PASSWORD from docs/DEPLOYMENT.md) goes through this
  # path, and consumers shell-source the file unquoted.
  check_line "${key}=${value}"
  printf '%s=%s\n' "${key}" "${value}" >> "${TMP_FILE}"
done

# Preserve operator-managed keys the generator does not own (e.g.
# DEVICE_COOKIE_SECRET, FLEET_API_TOKEN) so a re-run never drops them.
# Every preserved line is validated: a line outside the safe charset aborts
# the run (previous secrets.env stays intact) instead of passing shell
# metacharacters through to the consumers that source this file.
if [ -f "${SECRETS_FILE}" ]; then
  while IFS= read -r line; do
    case "${line}" in
      ''|'#'*) continue ;;
      *=*) ;;
      *) continue ;;
    esac
    key=${line%%=*}
    case " ${GENERATED_KEYS} " in
      *" ${key} "*) continue ;;
    esac
    check_line "${line}"
    printf '%s\n' "${line}" >> "${TMP_FILE}"
  done < "${SECRETS_FILE}"
fi

# Atomic replace: write to a temp file, fix the mode, then rename. An
# interrupted run can never leave a truncated secrets.env behind.
chmod 600 "${TMP_FILE}"
mv -f "${TMP_FILE}" "${SECRETS_FILE}"
trap - EXIT INT TERM

# ── Per-key secret files (mounted as Docker secrets in the prod stack) ──────

for key in ${GENERATED_KEYS}; do
  lower=$(lower_key "${key}")
  value=$(value_of "${key}")
  tmp="${SECRETS_DIR}/.${lower}.tmp.$$"
  printf '%s\n' "${value}" > "${tmp}"
  chmod 0444 "${tmp}"
  mv -f "${tmp}" "${SECRETS_DIR}/${lower}"
done

# Operator-managed keys: mirror the hand-added value (or just a newline
# when unset — see header) so the Docker secrets always resolve.
for key in ${MIRRORED_KEYS}; do
  lower=$(lower_key "${key}")
  mirror_tmp="${SECRETS_DIR}/.${lower}.tmp.$$"
  printf '%s\n' "$(value_of "${key}")" > "${mirror_tmp}"
  chmod 0444 "${mirror_tmp}"
  mv -f "${mirror_tmp}" "${SECRETS_DIR}/${lower}"
done

chmod 600 "${SECRETS_FILE}"
echo "[secrets-init] wrote ${SECRETS_FILE} and ${SECRETS_DIR}/<key>"
