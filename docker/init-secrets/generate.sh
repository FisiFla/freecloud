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

set -e
# The secrets file (and every per-key file) must never be world-readable, not
# even for the window between creation and chmod.
umask 077

SECRETS_FILE=${SECRETS_FILE:-/run/freecloud/secrets.env}
SECRETS_DIR=${SECRETS_DIR:-/run/freecloud/files}

# Keys owned by the generator. Anything else in secrets.env is operator-managed
# and is copied through untouched.
GENERATED_KEYS="POSTGRES_PASSWORD KC_ADMIN_PASSWORD AUTH_SECRET SCIM_BEARER_TOKEN SETUP_TOKEN ACCESS_EVAL_TOKEN FLEET_WEBHOOK_SECRET PROVISIONING_MASTER_KEY"

# Operator-managed keys mirrored one-per-file (like the generated ones) so
# they can be mounted as Docker secrets. Unlike GENERATED_KEYS these are
# never created here: an unset key mirrors as an empty file.
MIRRORED_KEYS="SLACK_ALERT_WEBHOOK_URL FLEET_API_TOKEN"

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

if [ -z "${missing}" ] && [ "${files_ok}" -eq 1 ]; then
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
# keeps them off the host's other users.
chmod 700 "$(dirname "${SECRETS_FILE}")" 2>/dev/null || true

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
    echo "[secrets-init] generated ${key}"
  fi
  printf '%s=%s\n' "${key}" "${value}" >> "${TMP_FILE}"
done

# Preserve operator-managed keys the generator does not own (e.g.
# DEVICE_COOKIE_SECRET, FLEET_API_TOKEN) so a re-run never drops them.
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
