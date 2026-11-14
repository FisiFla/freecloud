#!/usr/bin/env bash
# nightly-backup-verify.sh — prove end-to-end that a backup bundle taken from
# a real prod-compose stack restores correctly.
#
# This is the cycle-25 manual drill, automated for the nightly CI job:
#   1. boot  — PROD_SMOKE_KEEP=1 scripts/prod-smoke.sh (the REAL production
#      stack on *.localhost, all smoke assertions incl. the setup-token path)
#   2. bundle — scripts/backup-bundle.sh --compose against that live stack,
#      then assert the bundle artifacts exist and the manifest carries no
#      secret values
#   3. verify-restore — restore the bundle's cluster.sql into a scratch
#      Postgres and assert row counts (scripts/verify-restore.sh), with a
#      containerized psql shim (see below)
#
# Usage:
#   bash scripts/nightly-backup-verify.sh
#   COMPOSE_PROJECT_NAME=fcnightly bash scripts/nightly-backup-verify.sh
#   NIGHTLY_KEEP=1 bash scripts/nightly-backup-verify.sh   # leave the stack + workdir up for poking
#
# Environment:
#   COMPOSE_PROJECT_NAME  Compose project for the prod stack (default
#                         "fcnightly"). Exported so prod-smoke, backup-bundle
#                         --compose and the teardown below all use it. CI sets
#                         it at the job level; humans proving this locally
#                         MUST pick a name that cannot clash with a live stack
#                         (teardown is `down -v` on exactly this project).
#   NIGHTLY_KEEP=1        Leave the compose stack and the workdir behind on
#                         exit (CI dumps compose logs from the kept stack,
#                         then tears it down). Default: `down -v`, remove the
#                         scratch container and delete the workdir.
#
# SAFETY: this script writes .env.prod and uses .secrets/ in the repo root
# (via prod-smoke.sh, which refuses non-*.localhost checkouts). Caddy binds
# host :80/:443 and the scratch Postgres binds 127.0.0.1:55432.
#
# WHY TEARDOWN USES ONLY `-f docker-compose.prod.yml` (no smoke override):
# `down` matches containers by project label, not by service definition, so
# the plain `-f/-p` form removes the whole stack including the override-
# touched frontend — this is what the cycle-25 manual drill used. The smoke
# override only alters the frontend service (extra_hosts + CA cert); it
# creates no extra services.
#
# WHY A psql SHIM INSTEAD OF apt-get: runner apt DNS has been flaky (see
# .github/workflows/browser-e2e.yml), so installing the postgres client is a
# hard no. The shim runs the repo-pinned postgres:16 client in a container
# instead. It forwards its arguments verbatim, which is exactly compatible
# with how verify-restore.sh invokes psql: it parses the DATABASE_URLs into
# --host/--port/--username/--dbname on the host with python3 and calls psql
# only with those flags plus -c or --file (never connection URLs, never a
# dump on stdin). The --file paths live under verify-restore's own mktemp
# dir, so TMPDIR is pointed at this script's workdir and the whole workdir
# is mounted into the shim container at the same absolute path (read-only;
# psql only reads it).
#
# CAVEAT (expected, NOT a failure): the freecloud table baselines (users,
# audit_logs, connected_apps, devices) are typically 0 here — the smoke
# setup provisions Keycloak-side state, and the backend tables stay empty.
# verify-restore still proves the round-trip: the restored scratch DB must
# match those baselines exactly, and the Keycloak section must restore a
# real schema with >= 1 realm.
#
# Exit code is non-zero if any stage fails; every stage prints a
# "RESULT PASS|FAIL <stage>" line either way (like rehearse-kc-upgrade.sh).
# Secret values are NEVER printed: the manifest check reports file names
# only, and the source DATABASE_URL is built into the environment, not logs.

set -euo pipefail
umask 077

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

COMPOSE_FILE="docker/docker-compose.prod.yml"
COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-fcnightly}"
export COMPOSE_PROJECT_NAME
NIGHTLY_KEEP="${NIGHTLY_KEEP:-0}"
# Same pin as docker/postgres/Dockerfile (and rehearse-kc-upgrade.sh).
PG_IMAGE="postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea"
SCRATCH_NAME="${COMPOSE_PROJECT_NAME}-scratch"
SCRATCH_PORT="55432"

NIGHTLY_TMP="$(mktemp -d)"
BUNDLES_OUT="${NIGHTLY_TMP}/bundles"
SHIM_DIR="${NIGHTLY_TMP}/shim"
mkdir -p "${BUNDLES_OUT}" "${SHIM_DIR}" "${NIGHTLY_TMP}/tmp"
# TMPDIR first, before any child mktemp runs: verify-restore's --file paths
# then land under this workdir (see the shim comment), and prod-smoke's
# SMOKE_TMP does too — so NIGHTLY_KEEP=1 keeps it alongside the kept stack,
# whose frontend bind-mounts the Caddy root cert out of it.
export TMPDIR="${NIGHTLY_TMP}/tmp"
BUNDLE_DIR=""

log() { printf 'nightly-backup-verify: %s\n' "$*"; }
fail() { printf 'nightly-backup-verify: FAIL: %s\n' "$*" >&2; exit 1; }

FAILURES=0
result() { # $1 = PASS|FAIL, $2 = stage, $3.. = evidence
  local status="$1" item="$2"; shift 2
  echo "RESULT $status $item $*"
  [ "$status" = "FAIL" ] && FAILURES=$((FAILURES + 1))
  return 0
}

teardown() {
  # The scratch container is ALWAYS removed: it is trivially reproducible
  # (verify-restore's own logs in the workdir are the evidence) and neither
  # compose down nor anything else would remove it.
  docker rm -f "${SCRATCH_NAME}" >/dev/null 2>&1 || true
  if [ "${NIGHTLY_KEEP}" = "1" ]; then
    log "NIGHTLY_KEEP=1: leaving stack up (project: ${COMPOSE_PROJECT_NAME}) and workdir ${NIGHTLY_TMP}"
    log "NIGHTLY_KEEP=1: to clean up: docker compose -p ${COMPOSE_PROJECT_NAME} -f ${COMPOSE_FILE} down -v && rm -rf ${NIGHTLY_TMP}"
    return 0
  fi
  # Plain -f/-p down: label-matched, needs no override and no .env.prod, and
  # is a harmless no-op when the boot stage never got a stack up.
  log "tearing down compose stack (project: ${COMPOSE_PROJECT_NAME}; down -v)"
  docker compose -p "${COMPOSE_PROJECT_NAME}" -f "${COMPOSE_FILE}" down -v || true
  rm -rf "${NIGHTLY_TMP}" || true
}
trap teardown EXIT

# ── Preflight ─────────────────────────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || fail "docker not found"
command -v python3 >/dev/null 2>&1 || fail "python3 not found (verify-restore parses DATABASE_URLs with it)"
docker compose version >/dev/null 2>&1 || fail "docker compose not available"
# The scratch Postgres needs this host port; fail fast with a clear message
# instead of a cryptic `docker run -p` bind error mid-drill.
if (exec 3<>/dev/tcp/127.0.0.1/${SCRATCH_PORT}) 2>/dev/null; then
  fail "127.0.0.1:${SCRATCH_PORT} is already in use — the scratch Postgres cannot bind it"
fi
# Idempotency: a previous killed run may have left the scratch container.
docker rm -f "${SCRATCH_NAME}" >/dev/null 2>&1 || true

# NOTE on error handling below: the stage functions run as `if` conditions,
# a context in which `set -e` does not fire inside function bodies, so every
# fallible command carries an explicit `|| return 1`.

stage_boot() {
  # Up to two attempts. Rationale: the committed prod postgres healthcheck
  # (interval 5s, retries 10, no start_period) can be outrun by a first-boot
  # initdb on a slow-fsync host (observed: a 30s shutdown checkpoint on
  # LUKS-backed storage pushed readiness to ~65s, past the ~50s budget, so
  # `up` aborted with "postgres is unhealthy"). The second attempt boots
  # from the now-initialized pgdata volume in seconds. This retries the
  # whole prod-smoke (build cache makes it cheap) because the stage's
  # contract is "all smoke assertions passed", not merely "stack up".
  # A genuine assertion failure fails deterministically on both attempts.
  log "stage boot: PROD_SMOKE_KEEP=1 bash scripts/prod-smoke.sh (project: ${COMPOSE_PROJECT_NAME})"
  local attempt
  for attempt in 1 2; do
    log "boot attempt ${attempt} of 2"
    if PROD_SMOKE_KEEP=1 bash scripts/prod-smoke.sh 2>&1 | tee -a "${NIGHTLY_TMP}/prod-smoke.log"; then
      return 0
    fi
    [ "${attempt}" -eq 1 ] || return 1
    log "boot attempt 1 failed; retrying once (the pgdata volume is kept, so init is already done)"
  done
  return 0
}

stage_bundle() {
  # BACKUP_PASSPHRASE is forced empty: the drill needs a plaintext bundle
  # (verify-restore reads cluster.sql straight from it).
  log "stage bundle: scripts/backup-bundle.sh --compose ${BUNDLES_OUT}"
  BACKUP_PASSPHRASE= bash scripts/backup-bundle.sh --compose "${BUNDLES_OUT}" 2>&1 | tee "${NIGHTLY_TMP}/backup-bundle.log" || return 1
  local bundle_dir
  bundle_dir="$(ls -dt "${BUNDLES_OUT}"/freecloud-bundle-*/ 2>/dev/null | head -n 1 || true)"
  [ -n "${bundle_dir}" ] || { log "bundle missing: no freecloud-bundle-* dir under ${BUNDLES_OUT}"; return 1; }
  [ -s "${bundle_dir}/cluster.sql" ] || { log "bundle incomplete: cluster.sql missing or empty"; return 1; }
  [ -s "${bundle_dir}/secrets.tar" ] || { log "bundle incomplete: secrets.tar missing or empty"; return 1; }
  [ -f "${bundle_dir}/manifest.txt" ] || { log "bundle incomplete: manifest.txt missing"; return 1; }
  log "bundle artifacts OK in ${bundle_dir}"
  # The manifest must never carry secret VALUES (names/sizes/hashes only).
  # Grep every per-key secret file's value out of the manifest with fixed-
  # string matching. Values are long random strings; anything shorter than
  # 12 chars is skipped to rule out substring false positives. The values
  # themselves are never printed — only the file name on failure.
  local checked secret_file secret_val
  checked=0
  for secret_file in "${REPO_ROOT}"/.secrets/files/*; do
    [ -f "${secret_file}" ] || continue
    secret_val="$(cat "${secret_file}")" || return 1
    [ "${#secret_val}" -ge 12 ] || continue
    checked=$((checked + 1))
    if grep -Fq -- "${secret_val}" "${bundle_dir}/manifest.txt"; then
      log "manifest leaks a secret value (matches .secrets/files/$(basename "${secret_file}"))"
      return 1
    fi
  done
  [ "${checked}" -ge 1 ] || { log "manifest check vacuous: no secret values found to grep for"; return 1; }
  log "manifest clean: ${checked} secret values checked, none present"
  BUNDLE_DIR="${bundle_dir}"
  return 0
}

stage_verify() {
  [ -n "${BUNDLE_DIR}" ] || { log "internal error: no bundle dir from the bundle stage"; return 1; }
  log "stage verify-restore: starting scratch Postgres as ${SCRATCH_NAME} on 127.0.0.1:${SCRATCH_PORT}"
  local scratch_pw
  scratch_pw="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')" || return 1
  [ -n "${scratch_pw}" ] || { log "could not generate a scratch password"; return 1; }
  docker run -d --name "${SCRATCH_NAME}" \
    -p "127.0.0.1:${SCRATCH_PORT}:5432" \
    -e POSTGRES_USER=postgres -e "POSTGRES_PASSWORD=${scratch_pw}" -e POSTGRES_DB=postgres \
    "${PG_IMAGE}" >/dev/null || return 1
  local deadline
  deadline=$((SECONDS + 90))
  while ! docker exec "${SCRATCH_NAME}" pg_isready -U postgres -d postgres >/dev/null 2>&1; do
    [ "${SECONDS}" -lt "${deadline}" ] || { log "scratch Postgres not ready within 90s"; return 1; }
    sleep 2
  done
  log "scratch Postgres ready"
  # psql shim (no apt-get — see header). Forwards "$@" verbatim into the
  # pinned client image; PGPASSWORD is inherited from the shim's own
  # environment (verify-restore exports it for scratch calls and sets it
  # inline for source calls). --network host reaches both the loopback
  # scratch port and the compose network's container IPs from the runner.
  cat >"${SHIM_DIR}/psql" <<EOF
#!/usr/bin/env bash
# Generated by scripts/nightly-backup-verify.sh. Never commit this file.
exec docker run --rm -i --network host \\
  -e PGPASSWORD="\${PGPASSWORD:-}" \\
  -v "${NIGHTLY_TMP}:${NIGHTLY_TMP}:ro" \\
  "${PG_IMAGE}" psql "\$@"
EOF
  chmod +x "${SHIM_DIR}/psql" || return 1
  # Source baselines come from the compose Postgres, which publishes no host
  # port — reach it by its container IP on the compose network (the cycle-25
  # drill's approach). The container is found via compose ps, not by an
  # assumed "<project>-postgres-1" name.
  local pg_cid pg_ip pg_pw
  pg_cid="$(docker compose -p "${COMPOSE_PROJECT_NAME}" -f "${COMPOSE_FILE}" ps -q postgres 2>/dev/null)" || return 1
  [ -n "${pg_cid}" ] || { log "compose postgres container not found (project ${COMPOSE_PROJECT_NAME})"; return 1; }
  pg_ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${pg_cid}")" || return 1
  [ -n "${pg_ip}" ] || { log "compose postgres container has no IP address"; return 1; }
  pg_pw="$(cat "${REPO_ROOT}/.secrets/files/postgres_password")" || return 1
  [ -n "${pg_pw}" ] || { log "postgres_password secret file is empty"; return 1; }
  # The smoke postgres password is 64-char hex (generate.sh gen()), so it is
  # URL-safe raw — but verify-restore does not URL-decode, so refuse rather
  # than build a DATABASE_URL that would silently misparse.
  case "${pg_pw}" in
    ''|*[!A-Za-z0-9._~-]*) log "postgres password is not URL-safe; refusing to build DATABASE_URL"; return 1 ;;
  esac
  log "source Postgres at ${pg_ip}:5432 (password from .secrets/files/postgres_password, not shown)"
  log "running verify-restore.sh on ${BUNDLE_DIR}cluster.sql (baselines may be 0 — expected, see header)"
  local verify_rc
  verify_rc=0
  DATABASE_URL="postgres://freecloud:${pg_pw}@${pg_ip}:5432/freecloud" \
    SCRATCH_DATABASE_URL="postgres://postgres:${scratch_pw}@127.0.0.1:${SCRATCH_PORT}/postgres" \
    PATH="${SHIM_DIR}:${PATH}" \
    bash scripts/verify-restore.sh "${BUNDLE_DIR}cluster.sql" 2>&1 | tee "${NIGHTLY_TMP}/verify-restore.log" || verify_rc=$?
  log "verify-restore.sh exit: ${verify_rc}"
  [ "${verify_rc}" -eq 0 ] || return 1
  return 0
}

# ── Run the stages ────────────────────────────────────────────────────────────
log "project: ${COMPOSE_PROJECT_NAME} (NIGHTLY_KEEP=${NIGHTLY_KEEP}); workdir ${NIGHTLY_TMP}"

if stage_boot; then
  result PASS boot "prod-smoke assertions passed (full log streamed above)"
else
  result FAIL boot "prod-smoke failed (full log streamed above)"
  exit 1
fi

if stage_bundle; then
  result PASS bundle "${BUNDLE_DIR} (cluster.sql + secrets.tar + clean manifest)"
else
  result FAIL bundle "backup-bundle --compose failed or bundle incomplete"
  exit 1
fi

if stage_verify; then
  result PASS verify-restore "row counts match source; Keycloak realm present"
else
  result FAIL verify-restore "row counts mismatch or Keycloak section unrestorable (log streamed above)"
  exit 1
fi

log "ALL 3 STAGES PASSED"
