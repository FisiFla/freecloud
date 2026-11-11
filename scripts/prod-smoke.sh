#!/usr/bin/env bash
# prod-smoke.sh — boot the REAL production stack (docker/docker-compose.prod.yml
# + docker/Caddyfile, exactly as committed) on *.localhost domains and
# smoke-test it through Caddy, including the first-run setup-token path.
#
# Usage:
#   bash scripts/prod-smoke.sh            # boot, assert, tear down (down -v)
#   PROD_SMOKE_KEEP=1 bash scripts/prod-smoke.sh   # leave the stack up
#   COMPOSE_PROJECT_NAME=fcprodsmoke bash scripts/prod-smoke.sh
#
# SAFETY: this script writes .env.prod and uses .secrets/ in the repo root.
# It REFUSES to run when an existing .env.prod points at non-*.localhost
# domains, so it can never clobber a real operator checkout. The *.localhost
# domains in .env.prod are the guard; there is no other state check.
#
# WHY A RUNTIME-GENERATED OVERRIDE (never committed, lives in a mktemp dir):
# with *.localhost sites Caddy serves certs from its internal CA (no ACME).
# Two pieces of production wiring that a real operator gets from public DNS
# and a public CA do not exist for *.localhost, and the committed compose
# file has no hook for them:
#   1. In-container DNS returns NXDOMAIN for auth.localhost (verified:
#      `docker run --rm busybox:1.36 nslookup auth.localhost` -> NXDOMAIN),
#      while the frontend does OIDC discovery against the PUBLIC issuer
#      (AUTH_KEYCLOAK_ISSUER=https://auth.localhost/realms/freecloud; prod
#      sets no AUTH_KEYCLOAK_INTERNAL_URL because a real public hostname
#      resolves from containers too). The backend talks to Keycloak over
#      the internal KEYCLOAK_URL=http://keycloak:8080 (post-#297 admin
#      lockdown) and needs no equivalent help.
#   2. The frontend (Node/Auth.js) does not trust Caddy's internal CA.
# The override therefore only adds the smoke-environment equivalents of
# public DNS + a trusted CA for the frontend: extra_hosts *.localhost ->
# host-gateway (the same hairpin a real deployment takes via its public IP
# back to Caddy's published :443), and Caddy's own extracted local root
# cert mounted via NODE_EXTRA_CA_CERTS. No committed compose or
# Caddyfile content is changed. To mint the CA before the frontend needs
# to trust it, Caddy is first started alone (--no-deps), its root cert is
# extracted, then the full stack is brought up with the same command
# `make prod-up` runs plus `-f <override>` (the Makefile target has no
# override hook, so the script cannot go through `make prod-up` itself;
# prod-secrets-init and prod-build below DO run through make).
# FLEET_URL is deliberately https://fleet.invalid: backend boot and
# /readyz must not require Fleet reachability.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

COMPOSE_FILE="docker/docker-compose.prod.yml"
KEEP="${PROD_SMOKE_KEEP:-0}"
READY_TIMEOUT_S="${PROD_SMOKE_READY_TIMEOUT_S:-300}"

# Makefile exports these itself (so generated 0600 secret files stay owned
# by the operator); export them here too because this script also invokes
# docker compose directly.
export FREECLOUD_UID="$(id -u)"
export FREECLOUD_GID="$(id -g)"
# COMPOSE_PROJECT_NAME: honoured if set by the caller (make and compose
# both pass it through); CI uses the default derived from the directory.

SMOKE_TMP="$(mktemp -d)"
OVERRIDE_FILE="${SMOKE_TMP}/docker-compose.prod-smoke.override.yml"
ROOT_CRT="${SMOKE_TMP}/caddy-root.crt"
BODY_FILE="${SMOKE_TMP}/body"

log() { printf 'prod-smoke: %s\n' "$*"; }
fail() { printf 'prod-smoke: FAIL: %s\n' "$*" >&2; exit 1; }

compose() { docker compose -f "${COMPOSE_FILE}" --env-file .env.prod "$@"; }
compose_ov() { docker compose -f "${COMPOSE_FILE}" -f "${OVERRIDE_FILE}" --env-file .env.prod "$@"; }

dump_logs() {
  log "dumping compose ps + service logs (last 100 lines each)"
  compose_ov ps || compose ps || true
  local svc
  for svc in backend keycloak frontend caddy; do
    printf '\n===== logs: %s =====\n' "${svc}"
    compose_ov logs --tail=100 "${svc}" 2>&1 || compose logs --tail=100 "${svc}" 2>&1 || true
  done
}

TEARDOWN_RAN=0
STACK_STARTED=0
teardown() {
  [ "${TEARDOWN_RAN}" = "1" ] && return 0
  TEARDOWN_RAN=1
  # Never run `down` for a stack this run did not start: the preflight
  # refusal above must be side-effect free even in a live operator checkout.
  [ "${STACK_STARTED}" = "1" ] || return 0
  if [ "${KEEP}" = "1" ]; then
    log "PROD_SMOKE_KEEP=1: leaving stack up (project: ${COMPOSE_PROJECT_NAME:-default})"
    return 0
  fi
  log "tearing down (down -v)"
  compose_ov down -v || compose down -v || true
}

on_exit() {
  local rc=$?
  if [ "${rc}" -ne 0 ] && [ "${STACK_STARTED}" = "1" ]; then
    dump_logs
  fi
  teardown
  if [ "${KEEP}" = "1" ] && [ "${STACK_STARTED}" = "1" ]; then
    # The kept containers bind-mount ROOT_CRT from SMOKE_TMP; deleting it
    # would break their next restart.
    log "PROD_SMOKE_KEEP=1: runtime override + Caddy root cert kept in ${SMOKE_TMP}"
  else
    rm -rf "${SMOKE_TMP}"
  fi
  exit "${rc}"
}
trap on_exit EXIT

# ── Preflight: never clobber a real operator checkout ────────────────────────
if [ -f .env.prod ]; then
  existing_domain="$(sed -n 's/^DASHBOARD_DOMAIN=//p' .env.prod | head -n 1)"
  case "${existing_domain}" in
    *.localhost) log "existing .env.prod already uses smoke domain ${existing_domain}; rewriting it" ;;
    *) fail ".env.prod exists with DASHBOARD_DOMAIN='${existing_domain:-<unset>}' (not *.localhost) — refusing to touch a non-smoke checkout" ;;
  esac
fi
command -v docker >/dev/null 2>&1 || fail "docker not found"
command -v curl >/dev/null 2>&1 || fail "curl not found"
command -v python3 >/dev/null 2>&1 || fail "python3 not found (used for JSON assertions)"
docker compose version >/dev/null 2>&1 || fail "docker compose not available"

# ── .env.prod from the example, with *.localhost smoke values ────────────────
cp .env.prod.example .env.prod
set_env() {
  # set_env KEY VALUE — replace (or append) KEY=... in .env.prod
  if grep -q "^$1=" .env.prod; then
    sed -i "s|^$1=.*|$1=$2|" .env.prod
  else
    printf '%s=%s\n' "$1" "$2" >>.env.prod
  fi
}
set_env DASHBOARD_DOMAIN app.localhost
set_env API_DOMAIN api.localhost
set_env KC_DOMAIN auth.localhost
set_env DASHBOARD_PUBLIC_URL https://app.localhost
set_env KC_PUBLIC_URL https://auth.localhost
set_env FLEET_URL https://fleet.invalid
set_env FLEET_API_TOKEN smoke-dummy
set_env POSTURE_CHECK_ENABLED true
# SMTP_* stay unset/commented, exactly as in the example.
log "wrote .env.prod (app.localhost / api.localhost / auth.localhost, FLEET_URL=https://fleet.invalid)"

# ── Real operator path: secrets, then source-build ───────────────────────────
make prod-secrets-init
# Source the generated secrets the same way make prod-up does, so compose can
# interpolate POSTGRES_PASSWORD for every invocation below (including build).
set -a
# shellcheck disable=SC1091
. .secrets/secrets.env
set +a

make prod-build

# ── Mint Caddy's internal CA (Caddy alone), extract its root cert ────────────
log "starting Caddy alone to mint its internal CA"
STACK_STARTED=1
compose up -d --no-deps caddy
CA_DEADLINE=$((SECONDS + 90))
while :; do
  if compose exec -T caddy cat /data/caddy/pki/authorities/local/root.crt >"${ROOT_CRT}" 2>/dev/null \
    && grep -q "BEGIN CERTIFICATE" "${ROOT_CRT}"; then
    break
  fi
  # Fallback: locate the root cert wherever this Caddy version stores it.
  found="$(compose exec -T caddy sh -c 'find /data -name root.crt 2>/dev/null | head -n 1' 2>/dev/null || true)"
  if [ -n "${found}" ] && compose exec -T caddy cat "${found}" >"${ROOT_CRT}" 2>/dev/null \
    && grep -q "BEGIN CERTIFICATE" "${ROOT_CRT}"; then
    log "Caddy root cert found at ${found} (non-default path)"
    break
  fi
  [ "${SECONDS}" -lt "${CA_DEADLINE}" ] || fail "could not extract Caddy local root cert within 90s"
  sleep 3
done
log "extracted Caddy local root cert ($(wc -c <"${ROOT_CRT}") bytes)"

# ── Runtime override: smoke DNS + CA trust only (see header) ─────────────────
cat >"${OVERRIDE_FILE}" <<EOF
# Generated by scripts/prod-smoke.sh at runtime. Never commit this file.
services:
  frontend:
    extra_hosts:
      - "app.localhost:host-gateway"
      - "api.localhost:host-gateway"
      - "auth.localhost:host-gateway"
    environment:
      NODE_EXTRA_CA_CERTS: /run/freecloud-smoke/caddy-root.crt
    volumes:
      - "${ROOT_CRT}:/run/freecloud-smoke/caddy-root.crt:ro"
EOF

log "bringing up the full production stack (prod-up equivalent + runtime override)"
compose_ov up -d

# ── curl through Caddy, trusting its internal CA ─────────────────────────────
# smoke_request HOST METHOD URL [curl args...] -> sets STATUS, body in BODY_FILE
smoke_request() {
  local host="$1" method="$2" url="$3"
  shift 3
  STATUS="$(curl -sS -o "${BODY_FILE}" -w '%{http_code}' \
    --cacert "${ROOT_CRT}" \
    --resolve "${host}:443:127.0.0.1" \
    -X "${method}" "$@" "${url}")"
}

# json_check PYTHON_EXPR — expr receives the parsed body as `d`; must be truthy.
json_check() {
  python3 -c '
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as e:
    print(f"invalid JSON: {e}", file=sys.stderr); sys.exit(1)
if not eval(sys.argv[2]):
    print(f"check failed: {sys.argv[2]} :: body={d!r}", file=sys.stderr); sys.exit(1)
' "${BODY_FILE}" "$1"
}

PASS_N=0
pass() { PASS_N=$((PASS_N + 1)); log "PASS ${PASS_N}: $1"; }
assert_status() { # expected description
  [ "${STATUS}" = "$1" ] || fail "$2: expected HTTP $1, got ${STATUS}; body: $(cat "${BODY_FILE}" 2>/dev/null)"
}

# ── Wait for readiness through Caddy ─────────────────────────────────────────
log "waiting up to ${READY_TIMEOUT_S}s for https://api.localhost/readyz"
DEADLINE=$((SECONDS + READY_TIMEOUT_S))
while :; do
  if smoke_request api.localhost GET https://api.localhost/readyz 2>/dev/null && [ "${STATUS}" = "200" ]; then
    log "readyz returned 200 after $((SECONDS))s of polling window"
    break
  fi
  if [ "${SECONDS}" -ge "${DEADLINE}" ]; then
    log "last readyz status: ${STATUS:-<none>}; body: $(cat "${BODY_FILE}" 2>/dev/null || true)"
    fail "readyz did not return 200 within ${READY_TIMEOUT_S}s"
  fi
  sleep 5
done

# ── Assertions (each fails the script loudly) ────────────────────────────────
smoke_request api.localhost GET https://api.localhost/healthz
assert_status 200 "GET /healthz"
pass "GET https://api.localhost/healthz -> 200"

smoke_request api.localhost GET https://api.localhost/readyz
assert_status 200 "GET /readyz"
json_check 'd["data"]["database"] == "ok" and d["data"]["keycloak"] == "ok"' \
  || fail "readyz body must report database ok + keycloak ok; body: $(cat "${BODY_FILE}")"
pass "GET https://api.localhost/readyz -> 200 with database ok + keycloak ok"

smoke_request api.localhost GET https://api.localhost/metrics
assert_status 404 "GET /metrics (Caddy rule)"
pass "GET https://api.localhost/metrics -> 404 (Caddy rule)"

smoke_request auth.localhost GET https://auth.localhost/realms/freecloud/.well-known/openid-configuration
assert_status 200 "GET OIDC discovery"
json_check 'd["issuer"] == "https://auth.localhost/realms/freecloud"' \
  || fail "OIDC issuer mismatch; body: $(cat "${BODY_FILE}")"
pass "OIDC discovery issuer == https://auth.localhost/realms/freecloud"

smoke_request auth.localhost GET https://auth.localhost/admin/
assert_status 404 "GET /admin/ (KC admin lockdown)"
pass "GET https://auth.localhost/admin/ -> 404 (KC admin lockdown)"

smoke_request api.localhost GET https://api.localhost/api/v1/setup/status
assert_status 200 "GET /api/v1/setup/status"
json_check 'd["data"]["provisioned"] is False and d["data"]["setupTokenRequired"] is True' \
  || fail "setup/status must be provisioned:false + setupTokenRequired:true; body: $(cat "${BODY_FILE}")"
pass "GET /api/v1/setup/status -> provisioned:false, setupTokenRequired:true"

SETUP_BODY_NO_TOKEN='{"adminEmail":"smoke-admin@example.com","adminPassword":"smoke-password-123","adminFirstName":"Smoke","adminLastName":"Admin","orgName":"Prod Smoke"}'
smoke_request api.localhost POST https://api.localhost/api/v1/setup \
  -H 'Content-Type: application/json' -d "${SETUP_BODY_NO_TOKEN}"
assert_status 403 "POST /api/v1/setup without setupToken"
pass "POST /api/v1/setup without setupToken -> 403"

SETUP_TOKEN_VALUE="$(sed -n 's/^SETUP_TOKEN=//p' .secrets/secrets.env | head -n 1)"
[ -n "${SETUP_TOKEN_VALUE}" ] || fail "SETUP_TOKEN not found in .secrets/secrets.env"
SETUP_BODY_WITH_TOKEN="$(SETUP_TOKEN_VALUE="${SETUP_TOKEN_VALUE}" python3 -c '
import json, os
print(json.dumps({
    "adminEmail": "smoke-admin@example.com",
    "adminPassword": "smoke-password-123",
    "adminFirstName": "Smoke",
    "adminLastName": "Admin",
    "orgName": "Prod Smoke",
    "setupToken": os.environ["SETUP_TOKEN_VALUE"],
}))')"
smoke_request api.localhost POST https://api.localhost/api/v1/setup \
  -H 'Content-Type: application/json' -d "${SETUP_BODY_WITH_TOKEN}"
assert_status 201 "POST /api/v1/setup with generated SETUP_TOKEN"
pass "POST /api/v1/setup with generated SETUP_TOKEN -> 201 (first-run path in prod config)"

smoke_request app.localhost GET https://app.localhost/api/version
assert_status 200 "GET /api/version"
json_check 'bool(d.get("version"))' \
  || fail "/api/version must return JSON with a version field; body: $(cat "${BODY_FILE}")"
pass "GET https://app.localhost/api/version -> 200 JSON with version field"

smoke_request app.localhost GET https://app.localhost/signin
assert_status 200 "GET /signin"
grep -qi '<html' "${BODY_FILE}" || fail "/signin did not return HTML; body starts: $(head -c 200 "${BODY_FILE}")"
pass "GET https://app.localhost/signin -> 200 HTML"

log "ALL ${PASS_N} ASSERTIONS PASSED"
