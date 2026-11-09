#!/usr/bin/env bash
# rehearse-kc-upgrade.sh — rehearse the in-place Keycloak 25 -> 26 upgrade on
# a synthesized, representative Keycloak 25 database.
#
# Why: cycle 22 shipped Keycloak 26.8.2 (#266) but the one-way Liquibase
# upgrade on an EXISTING 25 database was never rehearsed (CI e2e only covers
# a fresh DB). See docs/spikes/0002-keycloak-26-upgrade.md §3 (the
# "not exercised" list) and the addendum this script produced.
#
# What it does:
#   1. Builds the pre-upgrade 25 image (SPI + theme included) from a scratch
#      git worktree at the parent of the upgrade commit (KC25_REF, default
#      ac2ebbe^), and the current 26 image from docker/Dockerfile.keycloak.
#   2. Boots 25 against a throwaway Postgres 16 and seeds representative
#      state mirroring backend/internal/bootstrap: the "freecloud" realm,
#      the admin realm role, the four department groups, the
#      freecloud-service / freecloud-dashboard clients (+ SA realm-admin
#      grant), the browser-with-posture flow with the posture authenticator,
#      and two users with password credentials. Two documented divergences:
#      the dashboard client gets a localhost redirect URI (so the browser
#      flow can be driven) and an extra public "rehearsal-direct-grant"
#      client exists purely so a password-grant token can be checked
#      (bootstrap deliberately disables direct grants on its clients).
#   3. Records pre-upgrade identities/counts, stops 25, takes a pg_dump and
#      refuses to continue unless the dump is non-empty (the migration is
#      one-way; this mirrors the operator runbook).
#   4. Boots the 26 image against the same database and checks: Liquibase
#      changelog rows added, startup success, seeded realm/clients/users/
#      groups intact (ids + counts), posture SPI registered, a password-
#      grant token for a seeded user, and the kc-admin-api audience
#      behaviour on the migrated DB (service-account Admin call 401 before
#      the scope is assigned as default, 200 after).
#   5. Checks access-blocked.ftl under the 26 login themes: the provider jar
#      ships the template, every stock 26 login theme's template.ftl defines
#      registrationLayout, and a real browser-flow login against a deny
#      stub renders the access-blocked page.
#   6. Reboots 26 with KC_HOSTNAME + KC_HOSTNAME_PORT (the compose shape)
#      and checks the realm issuer carries the port and logins still work.
#
# Usage:
#   scripts/rehearse-kc-upgrade.sh
#   KEEP=1 scripts/rehearse-kc-upgrade.sh   # keep containers/workdir for poking
#
# Env knobs: KC_REHEARSE_PREFIX (docker name prefix, default
# kc2526-rehearsal), KC_PORT (host port, default 18081), KC25_REF (git ref
# of the pre-upgrade tree, default ac2ebbe^), WORKDIR (evidence dir).
#
# Requires: docker, git, curl, python3. Exit code is non-zero if any check
# fails; every check prints a "RESULT PASS|FAIL <item>" line either way.

set -euo pipefail
umask 077

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFIX="${KC_REHEARSE_PREFIX:-kc2526-rehearsal}"
KC_PORT="${KC_PORT:-18081}"
KC25_REF="${KC25_REF:-ac2ebbe^}"
KEEP="${KEEP:-0}"
WORKDIR="${WORKDIR:-$(mktemp -d /tmp/kc2526-rehearsal.XXXXXX)}"
mkdir -p "$WORKDIR"

NET="$PREFIX-net"
VOL="$PREFIX-pgdata"
PG_CONTAINER="$PREFIX-pg"
KC_CONTAINER="$PREFIX-kc"
STUB_CONTAINER="$PREFIX-stub"
IMG25="$PREFIX/kc:25"
IMG26="$PREFIX/kc:26"
# Same pin as docker/docker-compose.yml.
PG_IMAGE="postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea"

KC_BASE="http://127.0.0.1:$KC_PORT"
ADMIN_USER="admin"
ADMIN_PASS="rehearsal-admin-pass"
DB_USER="freecloud"
DB_PASS="freecloud"
REALM="freecloud"
SERVICE_SECRET="rehearsal-service-secret-0123456789abcdef"
ALICE_PASS="alice-rehearsal-password"
EVAL_TOKEN="rehearsal-token"

FAILURES=0
result() { # $1 = PASS|FAIL, $2 = item, $3.. = evidence
  local status="$1" item="$2"; shift 2
  echo "RESULT $status $item $*"
  [ "$status" = "FAIL" ] && FAILURES=$((FAILURES + 1))
  return 0
}
log() { echo "==> $*"; }

cleanup() {
  if [ "$KEEP" = "1" ]; then
    echo "KEEP=1: leaving containers ($KC_CONTAINER, $PG_CONTAINER, $STUB_CONTAINER),"
    echo "        network $NET, volume $VOL and evidence in $WORKDIR"
    return 0
  fi
  docker rm -f "$KC_CONTAINER" "$STUB_CONTAINER" "$PG_CONTAINER" >/dev/null 2>&1 || true
  docker volume rm "$VOL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  if [ -d "$WORKDIR/wt25" ]; then
    git -C "$REPO_ROOT" worktree remove --force "$WORKDIR/wt25" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# Idempotency: clear anything a previous (possibly KEEP=1) run left behind.
docker rm -f "$KC_CONTAINER" "$STUB_CONTAINER" "$PG_CONTAINER" >/dev/null 2>&1 || true
docker volume rm "$VOL" >/dev/null 2>&1 || true
docker network rm "$NET" >/dev/null 2>&1 || true
git -C "$REPO_ROOT" worktree remove --force "$WORKDIR/wt25" >/dev/null 2>&1 || true
git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1 || true

for tool in docker git curl python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "ERROR: $tool not on PATH" >&2; exit 1; }
done

# ---------------------------------------------------------------------------
# Python helper (Admin REST seed/snapshot/checks). Stdlib only.
# ---------------------------------------------------------------------------
cat > "$WORKDIR/kcapi.py" <<'PY'
import base64, json, sys, urllib.request, urllib.error, urllib.parse

BASE = sys.argv[1]; ADMIN_USER, ADMIN_PASS = sys.argv[2], sys.argv[3]
REALM = "freecloud"
SERVICE_CLIENT, DASH_CLIENT, GRANT_CLIENT = "freecloud-service", "freecloud-dashboard", "rehearsal-direct-grant"
SERVICE_SECRET = "rehearsal-service-secret-0123456789abcdef"
USERS = [("alice", "alice@freecloud.local", "alice-rehearsal-password"),
         ("bob", "bob@freecloud.local", "bob-rehearsal-password")]

def call(method, path, token=None, body=None, form=None):
    data, headers = None, {}
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    elif body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req) as r:
            raw = r.read().decode()
            return r.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try: raw = json.loads(raw)
        except Exception: pass
        return e.code, raw

def admin_token():
    st, tok = call("POST", "/realms/master/protocol/openid-connect/token",
                   form={"grant_type": "password", "client_id": "admin-cli",
                         "username": ADMIN_USER, "password": ADMIN_PASS})
    assert st == 200, f"admin token failed: {st} {tok}"
    return tok["access_token"]

def must(st, ok, what, extra=""):
    if st not in ok:
        raise SystemExit(f"SEED-FAIL {what}: HTTP {st} {extra}")

def seed(t):
    st, r = call("POST", "/admin/realms", t, body={
        "realm": REALM, "enabled": True, "displayName": "FreeCloud",
        "loginWithEmailAllowed": True, "duplicateEmailsAllowed": False,
        "resetPasswordAllowed": True, "editUsernameAllowed": True,
        "registrationAllowed": False, "accessTokenLifespan": 300})
    must(st, (201, 409), "create realm", r)
    st, r = call("POST", f"/admin/realms/{REALM}/roles", t, body={"name": "admin"})
    must(st, (201, 409), "create admin role", r)
    groups = {}
    for name in ("Engineering", "Marketing", "Sales", "Operations"):
        st, r = call("POST", f"/admin/realms/{REALM}/groups", t, body={"name": name})
        must(st, (201, 409), f"create group {name}", r)
        _, lst = call("GET", f"/admin/realms/{REALM}/groups?search={name}&exact=true", t)
        groups[name] = next(g["id"] for g in lst if g["name"] == name)
    st, r = call("POST", f"/admin/realms/{REALM}/clients", t, body={
        "clientId": SERVICE_CLIENT, "name": "FreeCloud Backend Service", "enabled": True,
        "publicClient": False, "serviceAccountsEnabled": True,
        "authorizationServicesEnabled": False, "standardFlowEnabled": False,
        "directAccessGrantsEnabled": False, "fullScopeAllowed": True, "secret": SERVICE_SECRET})
    must(st, (201, 409), "create service client", r)
    st, r = call("POST", f"/admin/realms/{REALM}/clients", t, body={
        "clientId": DASH_CLIENT, "name": "FreeCloud Dashboard", "enabled": True,
        "publicClient": True, "standardFlowEnabled": True, "implicitFlowEnabled": False,
        "directAccessGrantsEnabled": False, "serviceAccountsEnabled": False,
        "redirectUris": ["http://localhost/*"], "webOrigins": ["http://localhost"]})
    must(st, (201, 409), "create dashboard client", r)
    st, r = call("POST", f"/admin/realms/{REALM}/clients", t, body={
        "clientId": GRANT_CLIENT, "enabled": True, "publicClient": True,
        "standardFlowEnabled": False, "directAccessGrantsEnabled": True})
    must(st, (201, 409), "create direct-grant client", r)
    # Service-account realm-admin grant (mirrors ensureServiceAccountRoles).
    _, cls = call("GET", f"/admin/realms/{REALM}/clients?clientId={SERVICE_CLIENT}", t)
    svc_uuid = cls[0]["id"]
    _, sa = call("GET", f"/admin/realms/{REALM}/clients/{svc_uuid}/service-account-user", t)
    _, rms = call("GET", f"/admin/realms/{REALM}/clients?clientId=realm-management", t)
    rm_uuid = rms[0]["id"]
    _, role = call("GET", f"/admin/realms/{REALM}/clients/{rm_uuid}/roles/realm-admin", t)
    st, r = call("POST", f"/admin/realms/{REALM}/users/{sa['id']}/role-mappings/clients/{rm_uuid}",
                 t, body=[{"id": role["id"], "name": role["name"]}])
    must(st, (204,), "grant SA realm-admin", r)
    # Users (+ alice in Engineering, holding the admin realm role).
    _, admin_role = call("GET", f"/admin/realms/{REALM}/roles/admin", t)
    for username, email, pw in USERS:
        st, r = call("POST", f"/admin/realms/{REALM}/users", t, body={
            "username": username, "email": email, "firstName": username.title(),
            "lastName": "Rehearsal", "enabled": True, "emailVerified": True,
            "credentials": [{"type": "password", "value": pw, "temporary": False}]})
        must(st, (201, 409), f"create user {username}", r)
        _, lst = call("GET", f"/admin/realms/{REALM}/users?username={username}&exact=true", t)
        uid = lst[0]["id"]
        if username == "alice":
            st, r = call("PUT", f"/admin/realms/{REALM}/users/{uid}/groups/{groups['Engineering']}", t)
            must(st, (204,), "alice joins Engineering", r)
            st, r = call("POST", f"/admin/realms/{REALM}/users/{uid}/role-mappings/realm", t,
                         body=[{"id": admin_role["id"], "name": "admin"}])
            must(st, (204,), "alice admin role", r)
    # Posture flow (mirrors ensurePostureFlow): copy browser, add the
    # posture execution inside the forms sub-flow, REQUIRE it, lower it to
    # last, bind as the realm browser flow.
    st, r = call("POST", f"/admin/realms/{REALM}/authentication/flows/browser/copy", t,
                 body={"newName": "browser-with-posture"})
    must(st, (201, 409), "copy browser flow", r)
    forms = urllib.parse.quote("browser-with-posture forms")
    st, r = call("POST", f"/admin/realms/{REALM}/authentication/flows/{forms}/executions/execution",
                 t, body={"provider": "freecloud-posture-check"})
    must(st, (201, 409), "add posture execution", r)
    def forms_execs():
        _, ex = call("GET", f"/admin/realms/{REALM}/authentication/flows/{forms}/executions", t)
        return ex
    target = next(e for e in forms_execs() if e.get("providerId") == "freecloud-posture-check")
    target["requirement"] = "REQUIRED"
    st, r = call("PUT", f"/admin/realms/{REALM}/authentication/flows/{forms}/executions", t, body=target)
    must(st, (204,), "set posture execution REQUIRED", r)
    for _ in range(12):
        ex = forms_execs()
        tgt = next(e for e in ex if e.get("providerId") == "freecloud-posture-check")
        siblings = [e for e in ex if e.get("level") == tgt.get("level")]
        if siblings and siblings[-1]["id"] == tgt["id"]:
            break
        st, r = call("POST", f"/admin/realms/{REALM}/authentication/executions/{tgt['id']}/lower-priority", t)
        must(st, (204,), "lower posture execution", r)
    _, realm_rep = call("GET", f"/admin/realms/{REALM}", t)
    realm_rep["browserFlow"] = "browser-with-posture"
    st, r = call("PUT", f"/admin/realms/{REALM}", t, body=realm_rep)
    must(st, (204,), "bind browser flow", r)
    print("SEED-OK")

def snapshot(t):
    _, realm_rep = call("GET", f"/admin/realms/{REALM}", t)
    _, cls = call("GET", f"/admin/realms/{REALM}/clients?max=200", t)
    _, usr = call("GET", f"/admin/realms/{REALM}/users?max=200", t)
    _, grp = call("GET", f"/admin/realms/{REALM}/groups?max=200", t)
    _, scopes = call("GET", f"/admin/realms/{REALM}/client-scopes", t)
    _, info = call("GET", "/admin/serverinfo", t)
    forms = urllib.parse.quote("browser-with-posture forms")
    st, ex = call("GET", f"/admin/realms/{REALM}/authentication/flows/{forms}/executions", t)
    auth_providers = info.get("providers", {}).get("authenticator", {}).get("providers", {})
    login_themes = [th["name"] for th in info.get("themes", {}).get("login", [])]
    return {
        "version": info.get("systemInfo", {}).get("version"),
        "realm_id": realm_rep.get("id"), "browser_flow": realm_rep.get("browserFlow"),
        "clients": {c["clientId"]: c["id"] for c in cls if c["clientId"] in
                    (SERVICE_CLIENT, DASH_CLIENT, GRANT_CLIENT)},
        "users": {u["username"]: u["id"] for u in usr},
        "user_count": len(usr),
        "groups": {g["name"]: g["id"] for g in grp},
        "kc_admin_api_scope": any(s.get("name") == "kc-admin-api" for s in scopes),
        "posture_provider": "freecloud-posture-check" in auth_providers,
        "posture_in_flow": st == 200 and any(e.get("providerId") == "freecloud-posture-check" for e in ex),
        "login_themes": login_themes,
    }

def sa_token():
    st, tok = call("POST", f"/realms/{REALM}/protocol/openid-connect/token",
                   form={"grant_type": "client_credentials", "client_id": SERVICE_CLIENT,
                         "client_secret": SERVICE_SECRET})
    assert st == 200, f"SA token failed: {st} {tok}"
    return tok["access_token"]

def jwt_claims(tok):
    payload = tok.split(".")[1]
    payload += "=" * (-len(payload) % 4)
    return json.loads(base64.urlsafe_b64decode(payload))

def admin_api_check(t):
    before = sa_token()
    st_before, _ = call("GET", f"/admin/realms/{REALM}/users?max=1", before)
    aud_before = jwt_claims(before).get("aud")
    _, scopes = call("GET", f"/admin/realms/{REALM}/client-scopes", t)
    scope = next((s for s in scopes if s.get("name") == "kc-admin-api"), None)
    if scope is None:
        print(json.dumps({"scope_present": False, "before": st_before, "aud_before": aud_before}))
        return
    _, cls = call("GET", f"/admin/realms/{REALM}/clients?clientId={SERVICE_CLIENT}", t)
    st, r = call("PUT", f"/admin/realms/{REALM}/clients/{cls[0]['id']}/default-client-scopes/{scope['id']}", t)
    assert st == 204, f"assign kc-admin-api failed: {st} {r}"
    after = sa_token()
    st_after, _ = call("GET", f"/admin/realms/{REALM}/users?max=1", after)
    print(json.dumps({"scope_present": True, "before": st_before, "after": st_after,
                      "aud_before": aud_before, "aud_after": jwt_claims(after).get("aud")}))

def password_grant():
    st, tok = call("POST", f"/realms/{REALM}/protocol/openid-connect/token",
                   form={"grant_type": "password", "client_id": GRANT_CLIENT,
                         "username": "alice", "password": "alice-rehearsal-password"})
    if st != 200:
        print(json.dumps({"status": st, "body": tok})); return
    claims = jwt_claims(tok["access_token"])
    print(json.dumps({"status": st, "sub": claims.get("sub"),
                      "preferred_username": claims.get("preferred_username"), "iss": claims.get("iss")}))

cmd = sys.argv[4]
if cmd == "seed":
    seed(admin_token())
elif cmd == "snapshot":
    print(json.dumps(snapshot(admin_token()), indent=1, sort_keys=True))
elif cmd == "admin-api-check":
    admin_api_check(admin_token())
elif cmd == "password-grant":
    password_grant()
elif cmd == "compare":
    pre, post = json.load(open(sys.argv[5])), json.load(open(sys.argv[6]))
    bad = []
    for key in ("realm_id", "browser_flow", "clients", "users", "user_count", "groups"):
        if pre.get(key) != post.get(key):
            bad.append(f"{key}: {pre.get(key)} != {post.get(key)}")
    if bad:
        print("MISMATCH " + " | ".join(bad)); sys.exit(1)
    print("IDENTITIES-MATCH")
else:
    raise SystemExit(f"unknown cmd {cmd}")
PY
kcapi() { python3 "$WORKDIR/kcapi.py" "$KC_BASE" "$ADMIN_USER" "$ADMIN_PASS" "$@"; }

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
wait_kc() { # $1 = label
  local i
  for i in $(seq 1 90); do
    if curl -sf -o /dev/null "$KC_BASE/realms/master" 2>/dev/null; then
      log "$1 ready after ~$((i * 2))s"
      return 0
    fi
    sleep 2
  done
  echo "ERROR: $1 did not become ready; last logs:" >&2
  docker logs --tail 40 "$KC_CONTAINER" >&2 || true
  return 1
}

run_kc() { # $1 = image, $2 = boot label; remaining args = extra -e pairs
  local image="$1" label="$2"; shift 2
  docker rm -f "$KC_CONTAINER" >/dev/null 2>&1 || true
  # shellcheck disable=SC2068
  docker run -d --name "$KC_CONTAINER" --network "$NET" \
    -p "127.0.0.1:$KC_PORT:8080" \
    -e KC_DB=postgres \
    -e KC_DB_URL="jdbc:postgresql://$PG_CONTAINER:5432/keycloak" \
    -e KC_DB_DATABASE=keycloak \
    -e KC_DB_USERNAME="$DB_USER" \
    -e KC_DB_PASSWORD="$DB_PASS" \
    -e ACCESS_EVAL_URL="http://$STUB_CONTAINER:9000/evaluate" \
    -e ACCESS_EVAL_TOKEN="$EVAL_TOKEN" \
    $@ \
    "$image" start-dev >/dev/null
  wait_kc "$label"
}

changelog_count() {
  docker exec "$PG_CONTAINER" psql -U "$DB_USER" -d keycloak -tA \
    -c "SELECT count(*) FROM databasechangelog;" 2>/dev/null | tr -d '[:space:]'
}

# ---------------------------------------------------------------------------
# Build both images
# ---------------------------------------------------------------------------
log "building Keycloak 25 image from $KC25_REF (scratch worktree)"
git -C "$REPO_ROOT" worktree add --detach "$WORKDIR/wt25" "$KC25_REF" >/dev/null
grep -m1 'FROM quay.io/keycloak' "$WORKDIR/wt25/docker/Dockerfile.keycloak" | sed 's/^/    25 pin: /'
docker build -q -f "$WORKDIR/wt25/docker/Dockerfile.keycloak" -t "$IMG25" "$WORKDIR/wt25" >/dev/null
log "built $IMG25"

log "building current Keycloak 26 image from HEAD"
grep -m1 'FROM quay.io/keycloak' "$REPO_ROOT/docker/Dockerfile.keycloak" | sed 's/^/    26 pin: /'
docker build -q -f "$REPO_ROOT/docker/Dockerfile.keycloak" -t "$IMG26" "$REPO_ROOT" >/dev/null
log "built $IMG26"

# ---------------------------------------------------------------------------
# Postgres + Keycloak 25, seeded
# ---------------------------------------------------------------------------
docker network create "$NET" >/dev/null
docker volume create "$VOL" >/dev/null
docker run -d --name "$PG_CONTAINER" --network "$NET" \
  -e POSTGRES_USER="$DB_USER" -e POSTGRES_PASSWORD="$DB_PASS" -e POSTGRES_DB=keycloak \
  -v "$VOL:/var/lib/postgresql/data" "$PG_IMAGE" >/dev/null
for i in $(seq 1 30); do
  docker exec "$PG_CONTAINER" pg_isready -U "$DB_USER" -d keycloak >/dev/null 2>&1 && break
  sleep 1
done

log "booting Keycloak 25 (pre-rename KEYCLOAK_ADMIN* envs, posture disabled)"
run_kc "$IMG25" "Keycloak 25" \
  -e KEYCLOAK_ADMIN="$ADMIN_USER" \
  -e KEYCLOAK_ADMIN_PASSWORD="$ADMIN_PASS" \
  -e POSTURE_CHECK_ENABLED=false
docker logs "$KC_CONTAINER" > "$WORKDIR/kc25.log" 2>&1
grep -m1 'Keycloak .* started' "$WORKDIR/kc25.log" | sed 's/^/    /' || true

log "seeding representative state on 25"
kcapi seed
kcapi snapshot > "$WORKDIR/pre.json"
cat "$WORKDIR/pre.json"
PRE_CHANGELOG="$(changelog_count)"
log "pre-upgrade DATABASECHANGELOG rows: $PRE_CHANGELOG"

pre_spi=$(python3 -c "import json;print(json.load(open('$WORKDIR/pre.json'))['posture_provider'])")
[ "$pre_spi" = "True" ] && result PASS spi-on-25 "freecloud-posture-check registered on 25" \
                        || result FAIL spi-on-25 "provider missing on 25 (seed flow would be unfaithful)"
pre_scope=$(python3 -c "import json;print(json.load(open('$WORKDIR/pre.json'))['kc_admin_api_scope'])")
echo "    kc-admin-api scope present on 25: $pre_scope"

# ---------------------------------------------------------------------------
# Dump BEFORE upgrading (one-way migration), then boot 26 on the same DB
# ---------------------------------------------------------------------------
log "stopping 25 and taking pg_dump (rollback artefact)"
docker rm -f "$KC_CONTAINER" >/dev/null
docker exec "$PG_CONTAINER" pg_dump -U "$DB_USER" -d keycloak > "$WORKDIR/keycloak-25-pre-upgrade.sql"
if [ -s "$WORKDIR/keycloak-25-pre-upgrade.sql" ] && grep -q 'PostgreSQL database dump' "$WORKDIR/keycloak-25-pre-upgrade.sql"; then
  result PASS pg-dump "dump bytes: $(wc -c < "$WORKDIR/keycloak-25-pre-upgrade.sql")"
else
  result FAIL pg-dump "dump empty/invalid — refusing to rehearse a one-way migration without it"
  echo "Aborting before the 26 boot." >&2
  exit 1
fi

log "booting Keycloak 26 against the existing 25 database"
if run_kc "$IMG26" "Keycloak 26 (upgrade boot)" \
     -e KC_BOOTSTRAP_ADMIN_USERNAME="$ADMIN_USER" \
     -e KC_BOOTSTRAP_ADMIN_PASSWORD="$ADMIN_PASS" \
     -e POSTURE_CHECK_ENABLED=false; then
  result PASS kc26-boot "26 started on the migrated DB"
else
  result FAIL kc26-boot "26 did not start on the 25 DB (see kc26-boot.log)"
fi
docker logs "$KC_CONTAINER" > "$WORKDIR/kc26-boot.log" 2>&1
echo "    --- migration/startup evidence (kc26-boot.log) ---"
grep -iE 'liquibase|changelog|migrat|Keycloak .* started|ERROR' "$WORKDIR/kc26-boot.log" | head -20 | sed 's/^/    /' || true
POST_CHANGELOG="$(changelog_count)"
log "post-upgrade DATABASECHANGELOG rows: $POST_CHANGELOG (pre: $PRE_CHANGELOG)"
if [ -n "$PRE_CHANGELOG" ] && [ -n "$POST_CHANGELOG" ] && [ "$POST_CHANGELOG" -gt "$PRE_CHANGELOG" ]; then
  result PASS liquibase-upgrade "DATABASECHANGELOG $PRE_CHANGELOG -> $POST_CHANGELOG rows"
else
  result FAIL liquibase-upgrade "DATABASECHANGELOG $PRE_CHANGELOG -> $POST_CHANGELOG (expected growth)"
fi

# ---------------------------------------------------------------------------
# Post-upgrade checks on the migrated DB
# ---------------------------------------------------------------------------
kcapi snapshot > "$WORKDIR/post.json"
cat "$WORKDIR/post.json"
if kcapi compare "$WORKDIR/pre.json" "$WORKDIR/post.json"; then
  result PASS data-survival "realm/client/user/group ids and counts identical pre/post"
else
  result FAIL data-survival "pre/post snapshot mismatch (see pre.json/post.json)"
fi
post_spi=$(python3 -c "import json;d=json.load(open('$WORKDIR/post.json'));print(d['posture_provider'] and d['posture_in_flow'])")
[ "$post_spi" = "True" ] && result PASS spi-on-26 "provider registered and still in the migrated flow" \
                         || result FAIL spi-on-26 "provider/flow execution missing on 26"

log "password-grant token for seeded user alice (migrated credential)"
GRANT_JSON="$(kcapi password-grant)"; echo "    $GRANT_JSON"
echo "$GRANT_JSON" | grep -q '"status": 200' && result PASS token-flow "$GRANT_JSON" \
                                              || result FAIL token-flow "$GRANT_JSON"

log "kc-admin-api audience on the migrated DB (401 before scope, 200 after)"
AUD_JSON="$(kcapi admin-api-check)"; echo "    $AUD_JSON"
if echo "$AUD_JSON" | grep -q '"scope_present": true' \
   && echo "$AUD_JSON" | grep -q '"before": 401' \
   && echo "$AUD_JSON" | grep -q '"after": 200'; then
  result PASS kc-admin-api-migrated-db "$AUD_JSON"
else
  result FAIL kc-admin-api-migrated-db "$AUD_JSON"
fi

# ---------------------------------------------------------------------------
# access-blocked.ftl under the 26 login themes
# ---------------------------------------------------------------------------
log "checking access-blocked.ftl prerequisites inside the 26 image"
THEMES_JAR="$(docker exec "$KC_CONTAINER" sh -c 'ls /opt/keycloak/lib/lib/main/*keycloak-themes*.jar' 2>/dev/null | head -1)"
FTL_STRUCT="themes-jar not found"
if [ -n "$THEMES_JAR" ]; then
  docker cp "$KC_CONTAINER:$THEMES_JAR" "$WORKDIR/themes.jar" >/dev/null
  docker cp "$KC_CONTAINER:/opt/keycloak/providers/keycloak-posture-authenticator.jar" "$WORKDIR/provider.jar" >/dev/null
  FTL_STRUCT="$(python3 - "$WORKDIR/themes.jar" "$WORKDIR/provider.jar" <<'PY'
import sys, zipfile
themes, provider = zipfile.ZipFile(sys.argv[1]), zipfile.ZipFile(sys.argv[2])
ftl_in_provider = "theme-resources/templates/access-blocked.ftl" in provider.namelist()
layouts = {}
for name in themes.namelist():
    if name.endswith("/login/template.ftl"):
        theme = name.split("/")[1] if name.startswith("theme/") else name
        layouts[theme] = b"registrationLayout" in themes.read(name)
print(f"provider-ships-ftl={ftl_in_provider} template.ftl defining registrationLayout found in themes: {layouts} "
      "(keycloak and keycloak.v2 both declare parent=base, so both resolve a layout)")
sys.exit(0 if ftl_in_provider and layouts and all(layouts.values()) else 1)
PY
)" && FTL_OK=1 || FTL_OK=0
  echo "    $FTL_STRUCT"
  [ "$FTL_OK" = "1" ] && result PASS ftl-parent-layout "$FTL_STRUCT" \
                      || result FAIL ftl-parent-layout "$FTL_STRUCT"
else
  result FAIL ftl-parent-layout "$FTL_STRUCT"
fi

log "rendering access-blocked.ftl: browser login against a posture-deny stub"
docker run -d --name "$STUB_CONTAINER" --network "$NET" python:3.12-alpine python3 -c '
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        body = b"{\"success\":true,\"data\":{\"allow\":false,\"reasons\":[\"rehearsal-deny\"]}}"
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass
HTTPServer(("0.0.0.0", 9000), H).serve_forever()' >/dev/null
run_kc "$IMG26" "Keycloak 26 (posture-deny boot)" \
  -e KC_BOOTSTRAP_ADMIN_USERNAME="$ADMIN_USER" \
  -e KC_BOOTSTRAP_ADMIN_PASSWORD="$ADMIN_PASS" \
  -e POSTURE_CHECK_ENABLED=true
COOKIE_JAR="$WORKDIR/cookies.txt"
AUTH_URL="$KC_BASE/realms/$REALM/protocol/openid-connect/auth?client_id=freecloud-dashboard&redirect_uri=http%3A%2F%2Flocalhost%2Fcb&response_type=code&scope=openid&state=rehearsal"
curl -s -c "$COOKIE_JAR" "$AUTH_URL" -o "$WORKDIR/login-page.html"
LOGIN_ACTION="$(grep -o 'action="[^"]*"' "$WORKDIR/login-page.html" | head -1 | sed 's/action="//; s/"$//; s/&amp;/\&/g')"
if [ -n "$LOGIN_ACTION" ]; then
  curl -s -b "$COOKIE_JAR" -c "$COOKIE_JAR" -X POST "$LOGIN_ACTION" \
    --data-urlencode "username=alice" \
    --data-urlencode "password=$ALICE_PASS" \
    --data-urlencode "credentialId=" -o "$WORKDIR/blocked-page.html"
  THEME_USED="$(grep -o '/resources/[^/]*/login/[^/]*/' "$WORKDIR/blocked-page.html" | head -1 | awk -F/ '{print $5}' || true)"
  if grep -q 'Access denied: your device does not meet posture requirements' "$WORKDIR/blocked-page.html" \
     && grep -q 'rehearsal-deny' "$WORKDIR/blocked-page.html"; then
    result PASS ftl-render "access-blocked page rendered under login theme: ${THEME_USED:-unknown}"
  else
    result FAIL ftl-render "blocked page body not rendered; see blocked-page.html (login theme: ${THEME_USED:-unknown})"
    grep -oiE '<title>[^<]*|error[^<"]{0,60}' "$WORKDIR/blocked-page.html" | head -5 | sed 's/^/    /' || true
  fi
else
  result FAIL ftl-render "no login form found at the auth endpoint; see login-page.html"
fi
docker rm -f "$STUB_CONTAINER" >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# KC_HOSTNAME_PORT stack shape (compose: KC_HOSTNAME + KC_HOSTNAME_PORT)
# ---------------------------------------------------------------------------
log "rebooting 26 with KC_HOSTNAME=localhost KC_HOSTNAME_PORT=$KC_PORT"
run_kc "$IMG26" "Keycloak 26 (hostname-port boot)" \
  -e KC_BOOTSTRAP_ADMIN_USERNAME="$ADMIN_USER" \
  -e KC_BOOTSTRAP_ADMIN_PASSWORD="$ADMIN_PASS" \
  -e POSTURE_CHECK_ENABLED=false \
  -e KC_HOSTNAME=localhost \
  -e KC_HOSTNAME_PORT="$KC_PORT"
docker logs "$KC_CONTAINER" > "$WORKDIR/kc26-hostname.log" 2>&1
grep -iE 'hostname|ERROR' "$WORKDIR/kc26-hostname.log" | head -5 | sed 's/^/    /' || true
# With KC_HOSTNAME set, ask via that host name (127.0.0.1 is not the configured host).
ISSUER="$(curl -s "http://localhost:$KC_PORT/realms/$REALM/.well-known/openid-configuration" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("issuer",""))' 2>/dev/null || true)"
GRANT2_JSON="$(kcapi password-grant)"
if [ "$ISSUER" = "http://localhost:$KC_PORT/realms/$REALM" ] && echo "$GRANT2_JSON" | grep -q '"status": 200'; then
  result PASS kc-hostname-port "issuer=$ISSUER and password grant still 200"
else
  result FAIL kc-hostname-port "issuer=${ISSUER:-<none>} grant=$GRANT2_JSON"
fi

# ---------------------------------------------------------------------------
log "evidence in $WORKDIR (pre.json, post.json, *.log, *.html, keycloak-25-pre-upgrade.sql)"
if [ "$FAILURES" -eq 0 ]; then
  echo "REHEARSAL RESULT: ALL CHECKS PASSED"
else
  echo "REHEARSAL RESULT: $FAILURES CHECK(S) FAILED"
  exit 1
fi
