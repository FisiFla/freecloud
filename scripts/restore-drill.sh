#!/usr/bin/env bash
# restore-drill.sh — full wipe→restore→login drill for the backup bundle path.
#
# Boots a provisioned prod-shape stack (via scripts/prod-smoke.sh), takes a
# backup bundle, WIPES everything (containers, volumes, .secrets/, .env.prod),
# restores from the bundle ALONE following docs/BACKUP_RESTORE.md, boots the
# restored stack, and proves the pre-wipe admin can still sign in through the
# real Keycloak browser flow. A restore that never boots or never logs in is
# not a proven restore — this drill closes that gap.
#
# Usage:
#   bash scripts/restore-drill.sh
#   RESTORE_DRILL_KEEP=1 bash scripts/restore-drill.sh   # leave stack + scratch dirs
#   COMPOSE_PROJECT_NAME=myproj bash scripts/restore-drill.sh
#
# SAFETY: inherits prod-smoke.sh's *.localhost guard (it refuses to run when an
# existing .env.prod points at non-*.localhost domains). ALL Docker work uses
# COMPOSE_PROJECT_NAME (default fcrestoredrill) and tears down only that
# project — never the sim/dev stack. Binds host ports 80/443 through Caddy.
#
# Stages (each prints exactly one `RESULT PASS|FAIL: <stage>` line):
#   1. BOOT+SAVE  prod-smoke boot + setup; record pre-wipe facts (setup/status,
#                 admin Keycloak id via Admin API, org name via postgres).
#                 Retries prod-smoke ONCE when it fails before any migration
#                 completed (slow-disk postgres-init race — see stage code).
#   2. BUNDLE     backup-bundle.sh --compose (unencrypted: BACKUP_PASSPHRASE=).
#   3. WIPE       down -v, assert the project's volumes/containers are gone,
#                 move .secrets/ + .env.prod aside (total host loss).
#   4. RESTORE    restore .secrets/ + .env.prod FROM THE BUNDLE ONLY,
#                 prod-secrets-init (must be a no-op), postgres-only up,
#                 restore.sh via a docker psql shim (no apt-get), full stack up.
#   5. ASSERT-LOGIN  readyz, setup/status provisioned=true, admin id match,
#                 REAL browser-flow login: authorize -> login form -> POST
#                 credentials -> 302 with ?code= (password grant is disabled
#                 on the dashboard client, so grant_type=password is NOT used).
#
# Exit code is non-zero when any stage fails. On failure the teardown still
# runs unless RESTORE_DRILL_KEEP=1 (which leaves the stack + scratch dirs,
# including secret material under the drill temp dir, for forensics).
#
# Runtime override: stage 4 needs the same smoke-environment DNS/CA help as
# prod-smoke (see its header). prod-smoke.sh exposes no reusable pieces, so
# the small override-generation block below duplicates that logic; it is
# marked MIRRORS-PROD-SMOKE. This script never edits prod-smoke.sh.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

COMPOSE_FILE="docker/docker-compose.prod.yml"
# COMPOSE_PROJECT_NAME: honoured if set by the caller; the drill default keeps
# it away from every other stack on the host.
: "${COMPOSE_PROJECT_NAME:=fcrestoredrill}"
export COMPOSE_PROJECT_NAME
PROJ="${COMPOSE_PROJECT_NAME}"
KEEP="${RESTORE_DRILL_KEEP:-0}"
READY_TIMEOUT_S="${RESTORE_DRILL_READY_TIMEOUT_S:-300}"

# Same exports prod-smoke.sh makes before invoking compose/make directly.
FREECLOUD_UID="$(id -u)"
FREECLOUD_GID="$(id -g)"
export FREECLOUD_UID FREECLOUD_GID

# Exact credentials prod-smoke.sh provisions through the real setup path
# (SETUP_BODY_NO_TOKEN / SETUP_BODY_WITH_TOKEN); the drill logs in as this
# admin in stage 5.
SMOKE_ADMIN_EMAIL="smoke-admin@example.com"
SMOKE_ADMIN_PASSWORD="smoke-password-123"
EXPECTED_ORG="Prod Smoke"
DEFAULT_ORG_ID="00000000-0000-0000-0000-000000000001"

DRILL_TMP="$(mktemp -d)"
OVERRIDE_FILE="${DRILL_TMP}/docker-compose.restore-drill.override.yml"
ROOT_CRT="${DRILL_TMP}/caddy-root.crt"
BODY_FILE="${DRILL_TMP}/body"
JAR_FILE="${DRILL_TMP}/cookiejar"
SMOKE_LOG="${DRILL_TMP}/prod-smoke.log"
BUNDLE_OUT="${DRILL_TMP}/bundles"
HOST_LOSS_DIR="${DRILL_TMP}/host-loss-originals"
SHIM_DIR="${DRILL_TMP}/shim"
SECRET_SNAP_PRE="${DRILL_TMP}/secrets-sha.pre"
SECRET_SNAP_POST="${DRILL_TMP}/secrets-sha.post"
mkdir -p "${BUNDLE_OUT}" "${HOST_LOSS_DIR}" "${SHIM_DIR}"

log() { printf 'restore-drill: %s\n' "$*"; }
result() { printf 'RESULT %s: %s\n' "$1" "$2"; }
# stage_fail STAGE REASON — the exactly-one RESULT FAIL line, then exit.
stage_fail() {
  log "FAIL: $1: $2"
  result FAIL "$1"
  exit 1
}

compose() { docker compose -f "${COMPOSE_FILE}" --env-file .env.prod "$@" || return 1; }
compose_ov() { docker compose -f "${COMPOSE_FILE}" -f "${OVERRIDE_FILE}" --env-file .env.prod "$@" || return 1; }

STACK_STARTED=0
TEARDOWN_RAN=0
SMOKE_TMP_PARSED=""
SMOKE_TMP_FIRST_ATTEMPT=""
# rm_smoke_tmp DIR — remove one prod-smoke KEEP dir, only when it is exactly
# the marker-carrying dir under /tmp we parsed from its log.
rm_smoke_tmp() {
  case "$1" in
    /tmp/tmp.*)
      if [ -n "$1" ] && [ -f "$1/docker-compose.prod-smoke.override.yml" ]; then
        rm -rf "$1"
      fi
      ;;
  esac
}
teardown() {
  [ "${TEARDOWN_RAN}" = "1" ] && return 0
  TEARDOWN_RAN=1
  if [ "${KEEP}" = "1" ]; then
    log "RESTORE_DRILL_KEEP=1: leaving stack + scratch state (project: ${PROJ}, tmp: ${DRILL_TMP})"
    return 0
  fi
  log "tearing down project ${PROJ} (down -v) and removing ${DRILL_TMP}"
  # Best-effort down with every override this run may have used, then plain.
  # (Plain down suffices — the overrides only add a bind mount + env to the
  # frontend — but trying each keeps teardown robust if that ever changes.)
  if [ -n "${SMOKE_TMP_PARSED}" ] && [ -f "${SMOKE_TMP_PARSED}/docker-compose.prod-smoke.override.yml" ] && [ -f .env.prod ]; then
    docker compose -f "${COMPOSE_FILE}" -f "${SMOKE_TMP_PARSED}/docker-compose.prod-smoke.override.yml" --env-file .env.prod down -v >/dev/null 2>&1 || true
  fi
  if [ -f "${OVERRIDE_FILE}" ] && [ -f .env.prod ]; then
    compose_ov down -v >/dev/null 2>&1 || true
  fi
  if [ -f .env.prod ]; then
    compose down -v >/dev/null 2>&1 || true
  else
    # .env.prod was moved aside by WIPE and never restored (early failure):
    # down without --env-file still removes this project's containers/nets.
    docker compose -f "${COMPOSE_FILE}" down -v >/dev/null 2>&1 || true
  fi
  # prod-smoke's KEEP dirs (override + stale CA cert) are drill-run litter
  # once the stack is down (each attempt keeps its own).
  rm_smoke_tmp "${SMOKE_TMP_PARSED}"
  rm_smoke_tmp "${SMOKE_TMP_FIRST_ATTEMPT}"
  rm -rf "${DRILL_TMP}"
}

on_exit() {
  local rc=$?
  if [ "${rc}" -ne 0 ] && [ "${STACK_STARTED}" = "1" ] && [ "${KEEP}" != "1" ]; then
    log "failure teardown: compose ps for project ${PROJ}"
    if [ -f .env.prod ]; then
      compose ps || true
    fi
  fi
  teardown
  exit "${rc}"
}
trap on_exit EXIT

# ── Preflight ───────────────────────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || stage_fail "BOOT+SAVE" "docker not found"
command -v curl >/dev/null 2>&1 || stage_fail "BOOT+SAVE" "curl not found"
command -v python3 >/dev/null 2>&1 || stage_fail "BOOT+SAVE" "python3 not found (used for JSON assertions)"
docker compose version >/dev/null 2>&1 || stage_fail "BOOT+SAVE" "docker compose not available"

# ── curl through Caddy, trusting its internal CA (mirrors prod-smoke) ───────
# drill_request HOST METHOD URL [curl args...] -> sets STATUS, body in BODY_FILE
drill_request() {
  local host="$1" method="$2" url="$3"
  shift 3
  # Never fails: a curl error surfaces as STATUS=000 for the caller to assert
  # on, so every failure still prints its RESULT FAIL line under set -e.
  STATUS="$(curl -sS -o "${BODY_FILE}" -w '%{http_code}' \
    --cacert "${ROOT_CRT}" \
    --resolve "${host}:443:127.0.0.1" \
    -X "${method}" "$@" "${url}" 2>/dev/null)" || STATUS="000"
}

# json_check PYTHON_EXPR — expr receives the parsed body as `d`; must be truthy.
# Returns 1 (never trips set -e) so the caller prints its RESULT FAIL line.
json_check() {
  python3 -c '
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as e:
    print(f"invalid JSON: {e}", file=sys.stderr); sys.exit(1)
if not eval(sys.argv[2]):
    print(f"check failed: {sys.argv[2]} :: body={d!r}", file=sys.stderr); sys.exit(1)
' "${BODY_FILE}" "$1" || return 1
}

# extract_ca OUT — pull Caddy's local root cert out of the running caddy
# container (same approach as prod-smoke: default path, then find fallback).
extract_ca() {
  local out="$1"
  local deadline found
  deadline=$((SECONDS + 90))
  while :; do
    if compose exec -T caddy cat /data/caddy/pki/authorities/local/root.crt >"${out}" 2>/dev/null \
      && grep -q "BEGIN CERTIFICATE" "${out}"; then
      return 0
    fi
    found="$(compose exec -T caddy sh -c 'find /data -name root.crt 2>/dev/null | head -n 1' 2>/dev/null || true)"
    if [ -n "${found}" ] && compose exec -T caddy cat "${found}" >"${out}" 2>/dev/null \
      && grep -q "BEGIN CERTIFICATE" "${out}"; then
      log "Caddy root cert found at ${found} (non-default path)"
      return 0
    fi
    [ "${SECONDS}" -lt "${deadline}" ] || return 1
    sleep 3
  done
}

# write_override — MIRRORS-PROD-SMOKE: the runtime-only frontend DNS + CA
# trust help (extra_hosts *.localhost -> host-gateway, Caddy root via
# NODE_EXTRA_CA_CERTS). Never committed; lives in the drill temp dir.
write_override() {
  cat >"${OVERRIDE_FILE}" <<EOF
# Generated by scripts/restore-drill.sh at runtime. Never commit this file.
# Mirrors the override scripts/prod-smoke.sh generates (see its header).
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
}

# wait_readyz — poll https://api.localhost/readyz through Caddy until 200.
wait_readyz() {
  local deadline
  log "waiting up to ${READY_TIMEOUT_S}s for https://api.localhost/readyz"
  deadline=$((SECONDS + READY_TIMEOUT_S))
  while :; do
    if drill_request api.localhost GET https://api.localhost/readyz 2>/dev/null && [ "${STATUS}" = "200" ]; then
      log "readyz returned 200"
      return 0
    fi
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      log "last readyz status: ${STATUS:-<none>}; body: $(cat "${BODY_FILE}" 2>/dev/null || true)"
      return 1
    fi
    sleep 5
  done
}

# wait_postgres — poll pg_isready inside the postgres container until ok.
wait_postgres() {
  local deadline
  log "waiting up to 180s for postgres to accept connections"
  deadline=$((SECONDS + 180))
  while :; do
    if compose exec -T postgres pg_isready -U freecloud >/dev/null 2>&1; then
      log "postgres is ready"
      return 0
    fi
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      return 1
    fi
    sleep 3
  done
}

# wait_postgres_healthy — poll the postgres container's HEALTH STATUS (what
# `up -d` gates its dependents on) until it reads healthy.
wait_postgres_healthy() {
  local deadline id st
  log "waiting up to 180s for postgres to report healthy"
  deadline=$((SECONDS + 180))
  while :; do
    id="$(compose ps -q postgres 2>/dev/null | head -n 1)" || id=""
    if [ -n "${id}" ]; then
      st="$(docker inspect "${id}" --format '{{.State.Health.Status}}' 2>/dev/null)" || st=""
      if [ "${st}" = "healthy" ]; then
        log "postgres is healthy"
        return 0
      fi
    fi
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      return 1
    fi
    sleep 5
  done
}

# migrate_done_ok — true when the migrate one-shot already reached Exited(0).
# The backend gates on it and setup needs the backend, so Exited(0) means the
# stack may already be provisioned (prod-smoke is NOT re-runnable then: its
# setup POST expects a virgin 201, not a 409).
migrate_done_ok() {
  local id
  id="$(compose ps -q migrate 2>/dev/null | head -n 1)" || id=""
  [ -n "${id}" ] || return 1
  [ "$(docker inspect "${id}" --format '{{.State.Status}}' 2>/dev/null)" = "exited" ] || return 1
  [ "$(docker inspect "${id}" --format '{{.State.ExitCode}}' 2>/dev/null)" = "0" ]
}

# compose_net — the compose network the postgres container is attached to
# (robust to any COMPOSE_PROJECT_NAME, default or caller-set).
compose_net() {
  local cid
  cid="$(compose ps -q postgres 2>/dev/null | head -n 1)" || cid=""
  [ -n "${cid}" ] || return 1
  docker inspect "${cid}" --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' || return 1
}

# db_exec SQL — run a read query against freecloud via compose exec (the
# container's local-socket trust auth needs no password).
db_exec() {
  compose exec -T postgres psql -U freecloud -d freecloud -X -tAc "$1" 2>/dev/null || return 1
}

# kc_admin_user EMAIL — print "<keycloak-user-id>\n<required-actions-json>"
# for EMAIL in the freecloud realm, via the master-realm Admin API.
# Keycloak publishes no ports in the prod compose file, so this runs from a
# throwaway python container on the compose network (auto-removed, --rm).
# Master credentials: user `admin` (compose KC_BOOTSTRAP_ADMIN_USERNAME) +
# KC_ADMIN_PASSWORD from .secrets/secrets.env — read from files, never echoed.
kc_admin_user() {
  local email="$1" pw net py_out rc
  pw="$(sed -n 's/^KC_ADMIN_PASSWORD=//p' .secrets/secrets.env | head -n 1)"
  [ -n "${pw}" ] || return 1
  net="$(compose_net)" || return 1
  [ -n "${net}" ] || return 1
  printf '%s' "${pw}" >"${DRILL_TMP}/kc.pw"
  chmod 600 "${DRILL_TMP}/kc.pw"
  cat >"${DRILL_TMP}/kc_user.py" <<'PYEOF'
import json, sys, urllib.parse, urllib.request
email = sys.argv[1]
password = open("/kc.pw", encoding="utf-8").read().strip()
token_req = urllib.request.Request(
    "http://keycloak:8080/realms/master/protocol/openid-connect/token",
    data=urllib.parse.urlencode({
        "grant_type": "password", "client_id": "admin-cli",
        "username": "admin", "password": password,
    }).encode(),
    headers={"Content-Type": "application/x-www-form-urlencoded"})
with urllib.request.urlopen(token_req, timeout=30) as r:
    token = json.load(r)["access_token"]
users_req = urllib.request.Request(
    "http://keycloak:8080/admin/realms/freecloud/users?email="
    + urllib.parse.quote(email) + "&exact=true",
    headers={"Authorization": "Bearer " + token})
with urllib.request.urlopen(users_req, timeout=30) as r:
    users = json.load(r)
if not users:
    print("NO_SUCH_USER", file=sys.stderr); sys.exit(2)
print(users[0]["id"])
print(json.dumps(users[0].get("requiredActions") or []))
PYEOF
  if py_out="$(docker run --rm --network "${net}" \
    -v "${DRILL_TMP}/kc_user.py:/kc_user.py:ro" \
    -v "${DRILL_TMP}/kc.pw:/kc.pw:ro" \
    python:3.12-alpine python3 /kc_user.py "${email}" 2>"${DRILL_TMP}/kc_user.err")"; then
    rc=0
  else
    rc=$?
  fi
  rm -f "${DRILL_TMP}/kc.pw"
  if [ "${rc}" -ne 0 ]; then
    log "kc_admin_user: python one-shot failed: $(cat "${DRILL_TMP}/kc_user.err" 2>/dev/null | head -c 300)"
    return 1
  fi
  printf '%s\n' "${py_out}"
}

# pick_free_subnet — print the first candidate /24 whose address space appears
# in neither Docker's networks nor the host routing table. The prod compose
# file pins its network to ${FREECLOUD_SUBNET:-172.30.0.0/24}, which collides
# whenever another project on the host already owns that /24 — the drill must
# coexist with (never touch) those stacks, so it picks its own free /24.
pick_free_subnet() {
  local used cand prefix
  used="$(docker network ls -q | xargs docker network inspect --format '{{range .IPAM.Config}}{{.Subnet}} {{end}}' 2>/dev/null)"
  if command -v ip >/dev/null 2>&1; then
    used="${used} $(ip -o route 2>/dev/null || true)"
  fi
  for cand in 172.28.0.0/24 172.29.0.0/24 172.31.0.0/24 172.26.0.0/24 172.27.0.0/24 10.89.0.0/24 10.90.0.0/24; do
    prefix="${cand%.*}"
    if ! printf '%s\n' "${used}" | grep -qF "${prefix}"; then
      printf '%s\n' "${cand}"
      return 0
    fi
  done
  return 1
}

# The drill shell exports FREECLOUD_SUBNET for the whole run, so every compose
# invocation (prod-smoke's, the drill's, restore's) interpolates the same
# network AND the backend's TRUSTED_PROXY_CIDRS stays in step with it. A
# caller-set value is respected as-is.
if [ -z "${FREECLOUD_SUBNET:-}" ]; then
  FREECLOUD_SUBNET="$(pick_free_subnet)" \
    || stage_fail "BOOT+SAVE" "no free /24 found for FREECLOUD_SUBNET"
  log "auto-picked FREECLOUD_SUBNET=${FREECLOUD_SUBNET} (export FREECLOUD_SUBNET=... to override)"
fi
export FREECLOUD_SUBNET

# ── Stage 1: BOOT+SAVE ──────────────────────────────────────────────────────
# parse_smoke_tmp — best-effort extraction of prod-smoke's kept tmp dir from
# its log (needed for WIPE teardown + litter removal, on success AND failure).
parse_smoke_tmp() {
  SMOKE_TMP_PARSED="$(sed -n 's/.*runtime override + Caddy root cert kept in //p' "${SMOKE_LOG}" 2>/dev/null | head -n 1 | tr -d '[:space:]')"
}
run_smoke_once() {
  if COMPOSE_PROJECT_NAME="${PROJ}" PROD_SMOKE_KEEP=1 bash scripts/prod-smoke.sh >"${SMOKE_LOG}" 2>&1; then
    SMOKE_OK=1
  else
    SMOKE_OK=0
  fi
  STACK_STARTED=1
  parse_smoke_tmp
}
stage_boot_save() {
  log "stage BOOT+SAVE: booting a provisioned stack via prod-smoke.sh (project ${PROJ})"
  run_smoke_once
  # One retry for the slow-disk first-boot race ONLY: prod-smoke brings every
  # service up at once, and the prod postgres healthcheck has no start_period,
  # so on a slow disk initdb is still running when the 10x5s health budget
  # expires — `up -d` then aborts with "dependency failed to start" before
  # anything is provisioned. The retry is gated on migrate NEVER having
  # completed (nothing could be provisioned: backend gates on migrate, setup
  # needs the backend), so re-running prod-smoke from scratch is sound — its
  # setup POST still meets a virgin stack. A post-migration failure is never
  # retried (the retry's setup POST would 409 and mask the real error).
  if [ "${SMOKE_OK}" != "1" ] && [ -f .env.prod ] \
    && [ -n "$(compose ps -q postgres 2>/dev/null)" ] && ! migrate_done_ok; then
    log "prod-smoke failed before any migration completed; waiting for postgres to turn healthy, then retrying prod-smoke ONCE"
    cp "${SMOKE_LOG}" "${DRILL_TMP}/prod-smoke.log.1"
    SMOKE_TMP_FIRST_ATTEMPT="${SMOKE_TMP_PARSED}"
    if ! wait_postgres_healthy; then
      log "postgres never turned healthy; last 20 lines of its log:"
      compose logs --tail=20 postgres >&2 || true
      stage_fail "BOOT+SAVE" "postgres never became healthy after failed prod-smoke (first log: ${DRILL_TMP}/prod-smoke.log.1)"
    fi
    log "retrying prod-smoke.sh (attempt 2 of 2)"
    run_smoke_once
  fi
  if [ "${SMOKE_OK}" != "1" ]; then
    # The smoke log lives in DRILL_TMP (removed by teardown), so surface the
    # actual failure here: prod-smoke's FAIL line plus context around it.
    log "prod-smoke.sh failed; FAIL lines with context:"
    grep -n "FAIL:" "${SMOKE_LOG}" | head -n 5 >&2 || true
    first_fail="$(grep -n "FAIL:" "${SMOKE_LOG}" | head -n 1 | cut -d: -f1)"
    if [ -n "${first_fail}" ] && [ "${first_fail}" -gt 1 ]; then
      sed -n "$((first_fail > 15 ? first_fail - 15 : 1)),$((first_fail + 5))p" "${SMOKE_LOG}" >&2 || true
    else
      tail -n 30 "${SMOKE_LOG}" >&2 || true
    fi
    stage_fail "BOOT+SAVE" "prod-smoke.sh exited non-zero (log: ${SMOKE_LOG})"
  fi
  log "prod-smoke.sh passed; full log: ${SMOKE_LOG}"

  # Reuse prod-smoke's kept override + CA for stage-1 reads and the WIPE
  # teardown. Fallback: extract the CA ourselves (the stack is up).
  if [ -n "${SMOKE_TMP_PARSED}" ] && [ -f "${SMOKE_TMP_PARSED}/caddy-root.crt" ]; then
    cp "${SMOKE_TMP_PARSED}/caddy-root.crt" "${ROOT_CRT}" \
      || stage_fail "BOOT+SAVE" "could not copy the prod-smoke CA cert"
    log "reusing prod-smoke CA from ${SMOKE_TMP_PARSED}"
  else
    log "could not parse prod-smoke tmp dir from log; extracting CA directly"
    SMOKE_TMP_PARSED=""
    extract_ca "${ROOT_CRT}" || stage_fail "BOOT+SAVE" "could not extract Caddy root cert"
  fi

  # Pre-wipe fact 1: setup/status through Caddy — must be provisioned now.
  drill_request api.localhost GET https://api.localhost/api/v1/setup/status \
    || stage_fail "BOOT+SAVE" "GET setup/status failed: ${STATUS:-<none>}"
  [ "${STATUS}" = "200" ] || stage_fail "BOOT+SAVE" "GET setup/status: expected 200, got ${STATUS}"
  cp "${BODY_FILE}" "${DRILL_TMP}/pre-status.json" \
    || stage_fail "BOOT+SAVE" "could not save pre-wipe setup/status"
  json_check 'd["data"]["provisioned"] is True' \
    || stage_fail "BOOT+SAVE" "pre-wipe setup/status must be provisioned:true; body: $(cat "${BODY_FILE}")"
  log "pre-wipe setup/status: $(cat "${DRILL_TMP}/pre-status.json")"

  # Pre-wipe fact 2: admin Keycloak id via Admin API (the setup POST response
  # carries no id, so this is the only pre-wipe source for it).
  KC_PRE="$(kc_admin_user "${SMOKE_ADMIN_EMAIL}")" \
    || stage_fail "BOOT+SAVE" "Admin API lookup failed for ${SMOKE_ADMIN_EMAIL}"
  ADMIN_ID_PRE="$(printf '%s\n' "${KC_PRE}" | sed -n '1p')"
  [ -n "${ADMIN_ID_PRE}" ] || stage_fail "BOOT+SAVE" "Admin API returned no id for ${SMOKE_ADMIN_EMAIL}"
  ADMIN_ACTIONS_PRE="$(printf '%s\n' "${KC_PRE}" | sed -n '2p')"
  log "pre-wipe admin id: ${ADMIN_ID_PRE} requiredActions: ${ADMIN_ACTIONS_PRE}"
  [ "${ADMIN_ACTIONS_PRE}" = "[]" ] || stage_fail "BOOT+SAVE" "pre-wipe admin has requiredActions ${ADMIN_ACTIONS_PRE}"

  # Pre-wipe fact 3: org name persisted by setup (organizations table).
  ORG_PRE="$(db_exec "SELECT name FROM organizations WHERE id = '${DEFAULT_ORG_ID}';" | tr -d '[:space:]')"
  # tr strips inner spaces too ("Prod Smoke" -> "ProdSmoke"); compare loosely.
  [ "${ORG_PRE}" = "ProdSmoke" ] || stage_fail "BOOT+SAVE" "pre-wipe org name: expected '${EXPECTED_ORG}', got '${ORG_PRE}'"
  log "pre-wipe org name: ${EXPECTED_ORG}"

  result PASS "BOOT+SAVE"
}

# ── Stage 2: BUNDLE ─────────────────────────────────────────────────────────
stage_bundle() {
  log "stage BUNDLE: taking an unencrypted backup bundle (--compose)"
  # The drill covers the unencrypted bundle shape; an inherited
  # BACKUP_PASSPHRASE must not silently switch it to encrypted.
  if ! env BACKUP_PASSPHRASE= bash scripts/backup-bundle.sh --compose "${BUNDLE_OUT}" >"${DRILL_TMP}/bundle.log" 2>&1; then
    tail -n 20 "${DRILL_TMP}/bundle.log" >&2 || true
    stage_fail "BUNDLE" "backup-bundle.sh --compose failed"
  fi
  BUNDLE_DIR="$(find "${BUNDLE_OUT}" -maxdepth 1 -name 'freecloud-bundle-*' -not -name '*.enc' -not -name '*.manifest.txt' | sort | tail -n 1)"
  [ -n "${BUNDLE_DIR}" ] && [ -d "${BUNDLE_DIR}" ] || stage_fail "BUNDLE" "no freecloud-bundle-* dir in ${BUNDLE_OUT}"
  for f in cluster.sql secrets.tar .env.prod manifest.txt; do
    [ -s "${BUNDLE_DIR}/${f}" ] || stage_fail "BUNDLE" "bundle is missing non-empty ${f}"
  done
  grep -q "env_prod: present" "${BUNDLE_DIR}/manifest.txt" \
    || stage_fail "BUNDLE" "bundle manifest does not record env_prod: present"
  log "bundle: ${BUNDLE_DIR} ($(du -sh "${BUNDLE_DIR}" | cut -f1))"
  result PASS "BUNDLE"
}

# ── Stage 3: WIPE ───────────────────────────────────────────────────────────
stage_wipe() {
  log "stage WIPE: destroying the stack, its volumes, and host secret state"
  # Down first (needs .env.prod for the --env-file compose helpers); try the
  # smoke override the stack was brought up with, then plain. Plain `down`
  # suffices — the override only touches the frontend — but belt and braces.
  if [ -n "${SMOKE_TMP_PARSED}" ] && [ -f "${SMOKE_TMP_PARSED}/docker-compose.prod-smoke.override.yml" ]; then
    docker compose -f "${COMPOSE_FILE}" -f "${SMOKE_TMP_PARSED}/docker-compose.prod-smoke.override.yml" --env-file .env.prod down -v >>"${DRILL_TMP}/wipe.log" 2>&1 || true
  fi
  compose down -v >>"${DRILL_TMP}/wipe.log" 2>&1 \
    || stage_fail "WIPE" "compose down -v failed"

  leftover_vols="$(docker volume ls --format '{{.Name}}' | grep "^${PROJ}_" || true)"
  [ -z "${leftover_vols}" ] || stage_fail "WIPE" "project volumes remain: ${leftover_vols}"
  leftover_ctrs="$(docker ps -aq --filter "label=com.docker.compose.project=${PROJ}" || true)"
  [ -z "${leftover_ctrs}" ] || stage_fail "WIPE" "project containers remain: ${leftover_ctrs}"
  log "project volumes and containers are gone"

  # Total host loss: the ONLY source of secrets/env from here on is the bundle.
  mv .secrets "${HOST_LOSS_DIR}/secrets" \
    || stage_fail "WIPE" "could not move .secrets aside"
  mv .env.prod "${HOST_LOSS_DIR}/env.prod" \
    || stage_fail "WIPE" "could not move .env.prod aside"
  [ ! -e .secrets ] && [ ! -e .env.prod ] || stage_fail "WIPE" ".secrets/.env.prod still present after move-aside"
  log "moved .secrets/ + .env.prod aside to ${HOST_LOSS_DIR}"
  result PASS "WIPE"
}

# snapshot_secrets OUT — sha256 of every file under .secrets/ (proves the
# restored secrets survive prod-secrets-init byte-identical).
snapshot_secrets() {
  (cd .secrets && find . -type f -exec sha256sum {} + | sort) >"$1"
}

# ── Stage 4: RESTORE ────────────────────────────────────────────────────────
stage_restore() {
  log "stage RESTORE: rebuilding the stack from the bundle alone"
  # Docs step 3, verbatim (BACKUP_RESTORE.md "Restore from a bundle on a
  # fresh host"): the bundle tar carries .secrets/ with relative paths.
  tar -xf "${BUNDLE_DIR}/secrets.tar" \
    || stage_fail "RESTORE" "could not extract secrets.tar from bundle"
  cp "${BUNDLE_DIR}/.env.prod" .env.prod \
    || stage_fail "RESTORE" "could not restore .env.prod from bundle"
  [ -f .secrets/secrets.env ] || stage_fail "RESTORE" "bundle secrets.tar has no secrets.env"
  for key in postgres_password kc_admin_password auth_secret scim_bearer_token access_eval_token fleet_api_token fleet_webhook_secret provisioning_master_key setup_token; do
    [ -s ".secrets/files/${key}" ] || stage_fail "RESTORE" "bundle secrets.tar is missing per-key file ${key}"
  done
  log "restored .secrets/ (incl. all per-key files) + .env.prod from bundle"

  # Docs step 4: with the restored .secrets/ in place this MUST be a no-op —
  # the generator never overwrites existing secrets. Assert byte-identity.
  snapshot_secrets "${SECRET_SNAP_PRE}" \
    || stage_fail "RESTORE" "could not snapshot restored secrets"
  if ! make prod-secrets-init >"${DRILL_TMP}/secrets-init.log" 2>&1; then
    tail -n 20 "${DRILL_TMP}/secrets-init.log" >&2 || true
    stage_fail "RESTORE" "make prod-secrets-init failed"
  fi
  snapshot_secrets "${SECRET_SNAP_POST}" \
    || stage_fail "RESTORE" "could not re-snapshot secrets after prod-secrets-init"
  cmp -s "${SECRET_SNAP_PRE}" "${SECRET_SNAP_POST}" \
    || stage_fail "RESTORE" "prod-secrets-init rewrote restored secrets (docs say it is a no-op)"
  grep -q "already complete" "${DRILL_TMP}/secrets-init.log" \
    || stage_fail "RESTORE" "prod-secrets-init did not report no-op on restored secrets"
  log "prod-secrets-init confirmed no-op: $(grep -m1 'already complete' "${DRILL_TMP}/secrets-init.log")"

  # Docs step 5: source secrets, postgres only, restore the cluster dump.
  # shellcheck disable=SC1091
  set -a
  . .secrets/secrets.env || stage_fail "RESTORE" "could not source restored secrets.env"
  set +a
  compose up -d postgres >"${DRILL_TMP}/pg-up.log" 2>&1 \
    || stage_fail "RESTORE" "could not bring up postgres alone"
  wait_postgres || stage_fail "RESTORE" "postgres never became ready"

  # restore.sh needs psql on PATH and an interactive YES. No apt-get: a
  # docker one-shot shim (postgres:16, matching the server major) on the
  # compose network, with the bundle dir mounted at the same path (restore.sh
  # passes --file=<host path>, which must resolve inside the shim too).
  # The PG password comes from the RESTORED per-key file — read, never echoed.
  PG_RESTORE_PW="$(tr -d '[:space:]' <.secrets/files/postgres_password)"
  [ -n "${PG_RESTORE_PW}" ] || stage_fail "RESTORE" "restored postgres_password file is empty"
  RESTORE_NET="$(compose_net)" || stage_fail "RESTORE" "could not discover the compose network"
  [ -n "${RESTORE_NET}" ] || stage_fail "RESTORE" "compose network name is empty"
  cat >"${SHIM_DIR}/psql" <<EOF
#!/usr/bin/env bash
exec docker run --rm -i --network "${RESTORE_NET}" -e PGPASSWORD \\
  -v "${BUNDLE_DIR}:${BUNDLE_DIR}:ro" \\
  postgres:16-alpine psql "\$@"
EOF
  chmod +x "${SHIM_DIR}/psql"
  log "restoring cluster.sql via restore.sh (shim psql on ${RESTORE_NET})"
  # RESTORE_DATABASE_URL must point at the postgres maintenance DB; the host
  # part is the in-network service name the shim resolves.
  if ! printf 'YES\n' | RESTORE_DATABASE_URL="postgres://freecloud:${PG_RESTORE_PW}@postgres:5432/postgres" \
      PATH="${SHIM_DIR}:${PATH}" bash scripts/restore.sh "${BUNDLE_DIR}/cluster.sql" \
      >"${DRILL_TMP}/restore.log" 2>&1; then
    tail -n 30 "${DRILL_TMP}/restore.log" >&2 || true
    stage_fail "RESTORE" "scripts/restore.sh failed"
  fi
  PG_RESTORE_PW=""
  grep -q "Restore complete" "${DRILL_TMP}/restore.log" \
    || stage_fail "RESTORE" "restore.sh log lacks the 'Restore complete' marker"
  log "cluster dump restored"

  # Full stack, the way prod-smoke brings it up: Caddy first (fresh CA — the
  # old caddy_data volume was wiped), extract its root cert, runtime override,
  # then everything (migrate runs against the restored DB, bootstrap is
  # idempotent against the restored realm).
  compose up -d --no-deps caddy >"${DRILL_TMP}/caddy-up.log" 2>&1 \
    || stage_fail "RESTORE" "could not start Caddy alone"
  extract_ca "${ROOT_CRT}" || stage_fail "RESTORE" "could not extract the new Caddy root cert"
  log "extracted new Caddy root cert ($(wc -c <"${ROOT_CRT}") bytes)"
  write_override || stage_fail "RESTORE" "could not write the runtime override"
  compose_ov up -d >"${DRILL_TMP}/stack-up.log" 2>&1 \
    || stage_fail "RESTORE" "could not bring up the restored stack"
  wait_readyz || stage_fail "RESTORE" "restored stack readyz never returned 200"
  result PASS "RESTORE"
}

# ── Stage 5: ASSERT-LOGIN ───────────────────────────────────────────────────
stage_assert_login() {
  log "stage ASSERT-LOGIN: proving the restored stack serves the pre-wipe admin"

  # (a) Health through Caddy.
  drill_request api.localhost GET https://api.localhost/readyz \
    || stage_fail "ASSERT-LOGIN" "GET readyz failed: ${STATUS:-<none>}"
  [ "${STATUS}" = "200" ] || stage_fail "ASSERT-LOGIN" "GET readyz: expected 200, got ${STATUS}"
  json_check 'd["data"]["database"] == "ok" and d["data"]["keycloak"] == "ok"' \
    || stage_fail "ASSERT-LOGIN" "readyz body must report database ok + keycloak ok; body: $(cat "${BODY_FILE}")"
  log "(a) readyz 200 with database ok + keycloak ok"

  # (b) Still provisioned after the wipe+restore.
  drill_request api.localhost GET https://api.localhost/api/v1/setup/status \
    || stage_fail "ASSERT-LOGIN" "GET setup/status failed: ${STATUS:-<none>}"
  [ "${STATUS}" = "200" ] || stage_fail "ASSERT-LOGIN" "GET setup/status: expected 200, got ${STATUS}"
  json_check 'd["data"]["provisioned"] is True' \
    || stage_fail "ASSERT-LOGIN" "post-restore setup/status must be provisioned:true; body: $(cat "${BODY_FILE}")"
  log "(b) setup/status provisioned:true"

  # Org name survived the round trip.
  ORG_POST="$(db_exec "SELECT name FROM organizations WHERE id = '${DEFAULT_ORG_ID}';" | tr -d '[:space:]')"
  [ "${ORG_POST}" = "ProdSmoke" ] || stage_fail "ASSERT-LOGIN" "post-restore org name: expected '${EXPECTED_ORG}', got '${ORG_POST}'"
  log "org name still '${EXPECTED_ORG}'"

  # Same Keycloak user id before and after (no orphaned/duplicated identity).
  KC_POST="$(kc_admin_user "${SMOKE_ADMIN_EMAIL}")" \
    || stage_fail "ASSERT-LOGIN" "Admin API lookup failed for ${SMOKE_ADMIN_EMAIL}"
  ADMIN_ID_POST="$(printf '%s\n' "${KC_POST}" | sed -n '1p')"
  [ -n "${ADMIN_ID_POST}" ] || stage_fail "ASSERT-LOGIN" "Admin API returned no post-restore id for ${SMOKE_ADMIN_EMAIL}"
  ADMIN_ACTIONS_POST="$(printf '%s\n' "${KC_POST}" | sed -n '2p')"
  log "post-restore admin id: ${ADMIN_ID_POST} requiredActions: ${ADMIN_ACTIONS_POST}"
  [ "${ADMIN_ACTIONS_POST}" = "[]" ] || stage_fail "ASSERT-LOGIN" "post-restore admin has requiredActions ${ADMIN_ACTIONS_POST}"
  [ "${ADMIN_ID_POST}" = "${ADMIN_ID_PRE}" ] \
    || stage_fail "ASSERT-LOGIN" "admin id changed across restore: pre=${ADMIN_ID_PRE} post=${ADMIN_ID_POST}"
  log "admin Keycloak id matches pre-wipe id"

  # (c) REAL login proof: drive the Keycloak browser flow with curl, exactly
  # like the Go SPI e2e (spi_client_ip_e2e_test.go attemptBrowserLogin).
  AUTHORIZE_URL="https://auth.localhost/realms/freecloud/protocol/openid-connect/auth?client_id=freecloud-dashboard&redirect_uri=https%3A%2F%2Fapp.localhost%2Fapi%2Fauth%2Fcallback%2Fkeycloak&response_type=code&scope=openid"
  rm -f "${JAR_FILE}"
  LOGIN_STATUS="$(curl -sS -o "${DRILL_TMP}/login.html" -w '%{http_code}' \
    --cacert "${ROOT_CRT}" --resolve "auth.localhost:443:127.0.0.1" \
    -c "${JAR_FILE}" "${AUTHORIZE_URL}" 2>/dev/null)" || LOGIN_STATUS="000"
  [ "${LOGIN_STATUS}" = "200" ] || stage_fail "ASSERT-LOGIN" "authorize GET: expected 200 login page, got ${LOGIN_STATUS}"
  ACTION_URL="$(python3 - "${DRILL_TMP}/login.html" <<'PYEOF'
import html, re, sys
page = open(sys.argv[1], encoding="utf-8", errors="replace").read()
m = re.search(r'id="kc-form-login"[^>]*action="([^"]+)"', page)
if not m:
    sys.exit(1)
print(html.unescape(m.group(1)))
PYEOF
)" || ACTION_URL=""
  [ -n "${ACTION_URL}" ] || stage_fail "ASSERT-LOGIN" "no kc-form-login action URL in authorize page: $(head -c 300 "${DRILL_TMP}/login.html")"
  case "${ACTION_URL}" in
    /.*) ACTION_URL="https://auth.localhost${ACTION_URL}" ;;
    https://auth.localhost/*) ;;
    *) stage_fail "ASSERT-LOGIN" "login form action points at an unexpected host: ${ACTION_URL}" ;;
  esac
  log "login form action: $(printf '%s' "${ACTION_URL}" | sed 's/?.*/?.../')"

  POST_OUT="$(curl -sS -o "${DRILL_TMP}/post.html" -w '%{http_code} %{redirect_url}' \
    --cacert "${ROOT_CRT}" --resolve "auth.localhost:443:127.0.0.1" \
    -b "${JAR_FILE}" -c "${JAR_FILE}" \
    --data-urlencode "username=${SMOKE_ADMIN_EMAIL}" \
    --data-urlencode "password=${SMOKE_ADMIN_PASSWORD}" \
    "${ACTION_URL}" 2>/dev/null)" || POST_OUT="000 "
  POST_STATUS="${POST_OUT%% *}"
  POST_LOCATION="${POST_OUT#* }"
  log "login POST -> ${POST_STATUS} location: ${POST_LOCATION:-<none>}"
  case "${POST_STATUS}" in
    302|303)
      case "${POST_LOCATION}" in
        *code=*) log "(c) browser login issued a code redirect" ;;
        *) stage_fail "ASSERT-LOGIN" "login POST redirected without code=: ${POST_LOCATION}" ;;
      esac
      ;;
    *)
      # Diagnose loudly: required-action re-render, posture access-blocked
      # page, or something else entirely — all FAIL, with the evidence.
      PAGE_URL="${ACTION_URL}"
      if grep -qiE 'required-action|update-profile|verify-profile|Update Profile|Verify Profile|update profile|verify your email' "${DRILL_TMP}/post.html"; then
        stage_fail "ASSERT-LOGIN" "login POST re-rendered a REQUIRED-ACTION page (status ${POST_STATUS}, url ${PAGE_URL})"
      fi
      if grep -qiE 'access-blocked|Access [Bb]locked|no enrolled device|user not found or disabled|device posture unavailable' "${DRILL_TMP}/post.html"; then
        REASONS="$(grep -oiE '(no enrolled device[^<]*|user not found[^<]*|device posture unavailable[^<]*|firewall disabled[^<]*|disk not encrypted[^<]*)' "${DRILL_TMP}/post.html" | head -n 3 | tr '\n' ';')"
        stage_fail "ASSERT-LOGIN" "login POST denied by the posture check (status ${POST_STATUS}): ${REASONS:-see post.html}; full page kept at ${DRILL_TMP}/post.html under RESTORE_DRILL_KEEP=1"
      fi
      TITLE="$(grep -oiE '<title>[^<]*</title>' "${DRILL_TMP}/post.html" | head -n 1)"
      stage_fail "ASSERT-LOGIN" "login POST returned ${POST_STATUS} (expected 302/303 with code=); ${TITLE:-no title}; page kept at ${DRILL_TMP}/post.html under RESTORE_DRILL_KEEP=1"
      ;;
  esac

  result PASS "ASSERT-LOGIN"
}

# ── Main ────────────────────────────────────────────────────────────────────
ADMIN_ID_PRE=""
ADMIN_ID_POST=""
ADMIN_ACTIONS_PRE=""
ADMIN_ACTIONS_POST=""
ORG_PRE=""
ORG_POST=""
BUNDLE_DIR=""
KC_PRE=""
KC_POST=""
SMOKE_OK=0
STATUS="000"

log "project: ${PROJ} (override with COMPOSE_PROJECT_NAME=...)"
stage_boot_save
stage_bundle
stage_wipe
stage_restore
stage_assert_login
log "ALL 5 STAGES PASSED"
