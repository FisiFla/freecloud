#!/usr/bin/env bash
# test-backup-integrity.sh — isolated integrity tests for the backup/restore path.
#
# Exercises the REAL scripts (copied byte-for-byte into a fixture checkout so
# the tests never touch the real .secrets/, .env.prod or backups/) and proves:
#   1. backup-bundle.sh fails closed without BACKUP_PASSPHRASE (unless
#      --allow-plaintext, which prints a loud warning)
#   2. encrypted bundle round trip works (backup -> verify -> decrypt)
#   3. tampered ciphertext is rejected BEFORE decryption (no output written)
#   4. tampered manifest / tampered .env.prod are rejected
#   5. a dump with a malicious database name is rejected before restore
#   6. a secrets.tar with a ../ member, an absolute path or a symlink is rejected
#   7. a legacy-format (old openssl, no MAC) bundle is refused without
#      --legacy and accepted with it
#   8. a restore whose live table counts differ from the manifest inventory fails
#   9. the safe secrets loader exports values literally and never evaluates them
#
# Postgres-backed restore tests run against a throwaway container (unique name,
# NO published port — reached via `docker exec` through a psql shim) that is
# removed afterwards. When docker is unavailable those tests SKIP; every
# file-level test always runs.
#
# Usage: bash scripts/test-backup-integrity.sh
#
# Requires: bash, openssl, python3, tar, sha256sum. Docker only for the
# restore tests (postgres:16-alpine, repo-pinned digest).

set -euo pipefail
umask 077

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Same pin as scripts/nightly-backup-verify.sh and scripts/rehearse-kc-upgrade.sh.
PG_IMAGE="postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea"

T="$(mktemp -d /tmp/fc-backup-integrity.XXXXXX)"
FIX="$T/fixture"
CTR="fc-backup-integrity-$$-$(date +%s)"
export FC_BIT_CTR="$CTR"
HAVE_DOCKER=0
command -v docker >/dev/null 2>&1 && HAVE_DOCKER=1

# Invoked through the EXIT trap below.
# shellcheck disable=SC2329
cleanup() {
  if [ "${HAVE_DOCKER}" -eq 1 ]; then
    docker rm -f "${CTR}" >/dev/null 2>&1 || true
  fi
  rm -rf "${T}"
}
trap cleanup EXIT

PASS=0
FAIL=0
SKIP=0
pass() { PASS=$((PASS + 1)); echo "PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "FAIL: $1" >&2; }
skip() { SKIP=$((SKIP + 1)); echo "SKIP: $1"; }

# ── Fixture checkout (scripts copied byte-for-byte from the real tree) ─────────
mkdir -p "${FIX}/scripts" "${FIX}/bin" "${FIX}/.secrets/files" \
  "${FIX}/docker/postgres" "${FIX}/backups"
cp "${REPO_ROOT}/scripts/"*.sh "${FIX}/scripts/"
chmod +x "${FIX}/scripts/"*.sh
cmp -s "${REPO_ROOT}/scripts/backup-bundle.sh" "${FIX}/scripts/backup-bundle.sh" \
  || { echo "FATAL: fixture copy diverged" >&2; exit 1; }

cat > "${FIX}/.secrets/secrets.env" <<'EOF'
POSTGRES_PASSWORD=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
KC_ADMIN_PASSWORD=fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210
AUTH_SECRET=YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY3ODkwK3Zvby9iYXI9PQ==
SCIM_BEARER_TOKEN=51ef9c2d8a4b4f0e9c1a2b3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f6
SETUP_TOKEN=setup-token-value-0123456789abcdef
ACCESS_EVAL_TOKEN=access-eval-token-0123456789abcdef
FLEET_WEBHOOK_SECRET=fleet-webhook-secret-0123456789abcdef
PROVISIONING_MASTER_KEY=cHJvdmlzaW9uaW5nLW1hc3Rlci1rZXktdm9uZy1iYXNlNjQrLz09
EOF
chmod 0600 "${FIX}/.secrets/secrets.env"
for k in postgres_password kc_admin_password auth_secret scim_bearer_token \
  access_eval_token fleet_api_token fleet_webhook_secret provisioning_master_key setup_token; do
  printf 'fixture-secret-%s-0123456789' "${k}" > "${FIX}/.secrets/files/${k}"
done
cat > "${FIX}/.env.prod" <<'EOF'
DASHBOARD_DOMAIN=app.example.com
API_DOMAIN=api.example.com
KC_DOMAIN=auth.example.com
FREECLOUD_VERSION=test
EOF
printf 'CREATE DATABASE keycloak;\n' > "${FIX}/docker/postgres/init-keycloak-db.sql"

# ── Canned cluster dumps ──────────────────────────────────────────────────────
# V1: backup-shape only (never restored into a live server).
cat > "${T}/dump-v1.sql" <<'EOF'
-- canned pg_dumpall output for backup tests
CREATE ROLE freecloud WITH LOGIN PASSWORD 'x';
CREATE DATABASE freecloud OWNER freecloud;
\connect freecloud
CREATE TABLE users (id SERIAL PRIMARY KEY, email TEXT);
CREATE TABLE devices (id SERIAL PRIMARY KEY);
\connect postgres
CREATE DATABASE keycloak OWNER freecloud;
\connect keycloak
CREATE TABLE realm (id SERIAL PRIMARY KEY, name TEXT);
EOF

# V2: actually restorable (used for the live restore round trip).
cat > "${T}/dump-v2.sql" <<'EOF'
DROP DATABASE IF EXISTS freecloud;
DROP DATABASE IF EXISTS keycloak;
CREATE DATABASE freecloud;
\connect freecloud
CREATE TABLE users (id SERIAL PRIMARY KEY, email TEXT);
INSERT INTO users (email) VALUES ('a@example.com');
CREATE TABLE devices (id SERIAL PRIMARY KEY);
\connect postgres
CREATE DATABASE keycloak;
\connect keycloak
CREATE TABLE realm (id SERIAL PRIMARY KEY, name TEXT);
INSERT INTO realm (name) VALUES ('freecloud');
EOF

# V3: V2 plus one CREATE TABLE that fails at restore time (dangling FK
# reference). The manifest inventory counts it from the dump text, but the
# live database will not have it — the inventory comparison must catch the
# error that ON_ERROR_STOP=0 swallows.
cat > "${T}/dump-v3.sql" <<'EOF'
DROP DATABASE IF EXISTS freecloud;
DROP DATABASE IF EXISTS keycloak;
CREATE DATABASE freecloud;
\connect freecloud
CREATE TABLE users (id SERIAL PRIMARY KEY, email TEXT);
CREATE TABLE devices (id SERIAL PRIMARY KEY);
CREATE TABLE broken_child (id INTEGER PRIMARY KEY, parent INTEGER REFERENCES missing_parent (id));
\connect postgres
CREATE DATABASE keycloak;
\connect keycloak
CREATE TABLE realm (id SERIAL PRIMARY KEY, name TEXT);
EOF

# pg_dumpall shim: backup.sh shells out to pg_dumpall; point it at a canned file.
cat > "${FIX}/bin/pg_dumpall" <<'EOF'
#!/usr/bin/env bash
# Test shim: emit the canned dump instead of contacting a server.
cat "${FC_CANNED_DUMP:?FC_CANNED_DUMP is not set}"
EOF
chmod +x "${FIX}/bin/pg_dumpall"
export PATH="${FIX}/bin:${PATH}"

export FC_CANNED_DUMP="${T}/dump-v1.sql"
export DATABASE_URL="postgres://u:p@127.0.0.1:5432/freecloud"
export BACKUP_RETENTION_DAYS=0

# ── 1. Fail closed without a passphrase ───────────────────────────────────────
echo "--- fail-closed behaviour ---"
if BACKUP_PASSPHRASE='' bash "${FIX}/scripts/backup-bundle.sh" "${FIX}/backups" \
    >"${T}/failclosed.log" 2>&1; then
  fail "direct bundle without passphrase unexpectedly succeeded"
else
  if grep -q "allow-plaintext" "${T}/failclosed.log"; then
    pass "direct bundle without passphrase aborts and names --allow-plaintext"
  else
    fail "fail-closed message does not name --allow-plaintext"
  fi
fi
if BACKUP_PASSPHRASE='' bash "${FIX}/scripts/backup-bundle.sh" --compose "${FIX}/backups" \
    >"${T}/failclosed-compose.log" 2>&1; then
  fail "--compose bundle without passphrase unexpectedly succeeded"
else
  if grep -q "allow-plaintext" "${T}/failclosed-compose.log"; then
    pass "--compose bundle without passphrase aborts before touching docker"
  else
    fail "--compose fail-closed message does not name --allow-plaintext"
  fi
fi

# ── --allow-plaintext works but warns loudly ──────────────────────────────────
PLAIN_OUT="${FIX}/backups-plain"
mkdir -p "${PLAIN_OUT}"
if BACKUP_PASSPHRASE='' bash "${FIX}/scripts/backup-bundle.sh" --allow-plaintext \
    "${PLAIN_OUT}" >"${T}/plain.log" 2>&1; then
  if grep -q "UNENCRYPTED" "${T}/plain.log"; then
    pass "--allow-plaintext succeeds with a loud UNENCRYPTED warning"
  else
    fail "--allow-plaintext succeeded but printed no UNENCRYPTED warning"
  fi
else
  fail "--allow-plaintext bundle failed: $(tail -n 3 "${T}/plain.log")"
fi
PLAIN_BUNDLE="$(find "${PLAIN_OUT}" -maxdepth 1 -name 'freecloud-bundle-*' | sort | tail -n 1)"
if [ -n "${PLAIN_BUNDLE}" ] && [ -f "${PLAIN_BUNDLE}/manifest.txt" ]; then
  pass "plaintext bundle directory published with manifest"
else
  fail "plaintext bundle directory missing"
  PLAIN_BUNDLE=""
fi
if [ -n "${PLAIN_BUNDLE}" ]; then
  if grep -q "^\.env\.prod\| \.env\.prod$" "${PLAIN_BUNDLE}/manifest.txt" \
    && grep -q "^inventory:" "${PLAIN_BUNDLE}/manifest.txt" \
    && grep -q "^freecloud 2$" "${PLAIN_BUNDLE}/manifest.txt" \
    && grep -q "^keycloak 1$" "${PLAIN_BUNDLE}/manifest.txt"; then
    pass "manifest hashes .env.prod and records per-database inventory"
  else
    fail "manifest lacks .env.prod hash or inventory; content: $(cat "${PLAIN_BUNDLE}/manifest.txt")"
  fi
  if bash "${FIX}/scripts/verify-bundle.sh" "${PLAIN_BUNDLE}" >"${T}/verify-plain.log" 2>&1; then
    pass "verify-bundle.sh accepts the untampered plaintext bundle"
  else
    fail "verify-bundle.sh rejected the untampered plaintext bundle: $(tail -n 3 "${T}/verify-plain.log")"
  fi
fi

# ── 2. Encrypted round trip ───────────────────────────────────────────────────
echo "--- encrypted round trip ---"
ENC_OUT="${FIX}/backups-enc"
mkdir -p "${ENC_OUT}"
export BACKUP_PASSPHRASE="fixture-passphrase-for-tests-only-0123456789"
if BACKUP_PASSPHRASE="${BACKUP_PASSPHRASE}" bash "${FIX}/scripts/backup-bundle.sh" \
    "${ENC_OUT}" >"${T}/enc.log" 2>&1; then
  pass "encrypted bundle creation succeeds"
else
  fail "encrypted bundle creation failed: $(tail -n 3 "${T}/enc.log")"
fi
ENC_FILE="$(find "${ENC_OUT}" -maxdepth 1 -name '*.tar.gz.enc' | sort | tail -n 1)"
ENC_MANIFEST="$(find "${ENC_OUT}" -maxdepth 1 -name '*.manifest.txt' | sort | tail -n 1)"
ENC_HMAC="$(find "${ENC_OUT}" -maxdepth 1 -name '*.hmac' | sort | tail -n 1)"
if [ -n "${ENC_FILE}" ] && [ -n "${ENC_MANIFEST}" ] && [ -n "${ENC_HMAC}" ]; then
  pass "encrypted bundle publishes ciphertext + manifest + MAC sidecar"
else
  fail "encrypted bundle artifacts incomplete (enc=${ENC_FILE} manifest=${ENC_MANIFEST} hmac=${ENC_HMAC})"
fi
if find "${ENC_OUT}" -maxdepth 1 -name 'freecloud-bundle-*' -not -name '*.enc' \
    -not -name '*.manifest.txt' -not -name '*.hmac' | grep -q .; then
  fail "plaintext bundle directory left behind after encryption"
else
  pass "no plaintext bundle directory left behind after encryption"
fi
DEC_OUT="${T}/decrypted"
if [ -n "${ENC_FILE}" ] && BACKUP_PASSPHRASE="${BACKUP_PASSPHRASE}" \
    bash "${FIX}/scripts/verify-bundle.sh" "${ENC_FILE}" "${DEC_OUT}" \
    >"${T}/decrypt.log" 2>&1; then
  pass "verify-bundle.sh decrypts the encrypted bundle after MAC verification"
else
  fail "verify-bundle.sh failed to decrypt: $(tail -n 3 "${T}/decrypt.log")"
fi
if [ -f "${DEC_OUT}/cluster.sql" ] && [ -f "${DEC_OUT}/secrets.tar" ] \
    && [ -f "${DEC_OUT}/.env.prod" ] && [ -f "${DEC_OUT}/manifest.txt" ]; then
  pass "decrypted bundle contains cluster.sql + secrets.tar + .env.prod + manifest"
else
  fail "decrypted bundle is incomplete"
fi
if [ -n "${ENC_MANIFEST}" ] && cmp -s "${ENC_MANIFEST}" "${DEC_OUT}/manifest.txt"; then
  pass "outer manifest is byte-identical to the authenticated inner manifest"
else
  fail "outer/inner manifest differ after authenticated decryption"
fi

# ── 3. Tampered ciphertext rejected BEFORE decryption ─────────────────────────
echo "--- tamper rejection ---"
if [ -z "${ENC_FILE}" ] || [ -z "${ENC_MANIFEST}" ] || [ -z "${ENC_HMAC}" ]; then
  fail "cannot run tamper tests: encrypted bundle artifacts incomplete"
else
  TAMPER_DIR="${T}/tamper-enc"
  mkdir -p "${TAMPER_DIR}"
  cp "${ENC_FILE}" "${TAMPER_DIR}/bundle.tar.gz.enc"
  cp "${ENC_MANIFEST}" "${TAMPER_DIR}/bundle.manifest.txt"
  cp "${ENC_HMAC}" "${TAMPER_DIR}/bundle.tar.gz.enc.hmac"
  # Flip one byte in the middle of the ciphertext.
  python3 - "${TAMPER_DIR}/bundle.tar.gz.enc" <<'PY'
import sys
p = sys.argv[1]
b = bytearray(open(p, "rb").read())
b[len(b) // 2] ^= 0x01
open(p, "wb").write(b)
PY
  if BACKUP_PASSPHRASE="${BACKUP_PASSPHRASE}" \
      bash "${FIX}/scripts/verify-bundle.sh" "${TAMPER_DIR}/bundle.tar.gz.enc" \
      "${TAMPER_DIR}/out" >"${T}/tamper-enc.log" 2>&1; then
    fail "tampered ciphertext was accepted"
  else
    if grep -qi "hmac\|integrity\|tamper\|mismatch" "${T}/tamper-enc.log" \
        && [ ! -e "${TAMPER_DIR}/out" ]; then
      pass "tampered ciphertext rejected before decryption (no output written)"
    else
      fail "ciphertext rejection left output behind or gave no integrity message"
    fi
  fi
  # Tampered outer manifest must be rejected too (manifest HMAC).
  MAN_DIR="${T}/tamper-manifest"
  mkdir -p "${MAN_DIR}"
  cp "${ENC_FILE}" "${MAN_DIR}/bundle.tar.gz.enc"
  cp "${ENC_MANIFEST}" "${MAN_DIR}/bundle.manifest.txt"
  cp "${ENC_HMAC}" "${MAN_DIR}/bundle.tar.gz.enc.hmac"
  echo "# attacker note" >> "${MAN_DIR}/bundle.manifest.txt"
  if BACKUP_PASSPHRASE="${BACKUP_PASSPHRASE}" \
      bash "${FIX}/scripts/verify-bundle.sh" "${MAN_DIR}/bundle.tar.gz.enc" \
      "${MAN_DIR}/out" >"${T}/tamper-manifest.log" 2>&1; then
    fail "tampered outer manifest was accepted"
  else
    pass "tampered outer manifest rejected via manifest HMAC"
  fi
fi

# ── 4. Tampered plaintext manifest / .env.prod rejected ───────────────────────
if [ -n "${PLAIN_BUNDLE}" ]; then
  HASH_DIR="${T}/tamper-hash"
  rm -rf "${HASH_DIR}"
  cp -r "${PLAIN_BUNDLE}" "${HASH_DIR}"
  chmod -R u+rw "${HASH_DIR}"
  printf 'tampered' >> "${HASH_DIR}/.env.prod"
  if bash "${FIX}/scripts/verify-bundle.sh" "${HASH_DIR}" >"${T}/tamper-env.log" 2>&1; then
    fail "bundle with tampered .env.prod was accepted"
  else
    if grep -qi "mismatch\|failed\|tamper" "${T}/tamper-env.log"; then
      pass "bundle with tampered .env.prod rejected (hash mismatch)"
    else
      fail "tampered .env.prod rejected without a clear message"
    fi
  fi
  MAN_DIR2="${T}/tamper-hashline"
  rm -rf "${MAN_DIR2}"
  cp -r "${PLAIN_BUNDLE}" "${MAN_DIR2}"
  chmod -R u+rw "${MAN_DIR2}"
  # Rewrite the recorded cluster.sql hash to all zeros.
  sed -i 's/^[0-9a-f]\{64\}  cluster\.sql$/0000000000000000000000000000000000000000000000000000000000000000  cluster.sql/' \
    "${MAN_DIR2}/manifest.txt"
  if bash "${FIX}/scripts/verify-bundle.sh" "${MAN_DIR2}" >"${T}/tamper-hashline.log" 2>&1; then
    fail "bundle with a tampered manifest hash line was accepted"
  else
    pass "bundle with a tampered manifest hash line rejected"
  fi
else
  skip "tampered .env.prod (no plaintext bundle was produced)"
  skip "tampered manifest hash (no plaintext bundle was produced)"
fi

# ── 5. Malicious database names rejected before any restore ───────────────────
echo "--- malicious database names ---"
cat > "${T}/evil-upper.sql" <<'EOF'
CREATE DATABASE freecloud;
CREATE DATABASE pwned'; SELECT pg_sleep(10); -- ;
\connect freecloud
CREATE TABLE users (id SERIAL PRIMARY KEY);
EOF
cat > "${T}/evil-lower.sql" <<'EOF'
create database "evil-db";
CREATE DATABASE freecloud;
\connect freecloud
CREATE TABLE users (id SERIAL PRIMARY KEY);
EOF
cat > "${T}/good-names.sql" <<'EOF'
CREATE DATABASE freecloud;
CREATE DATABASE "keycloak";
\connect freecloud
CREATE TABLE users (id SERIAL PRIMARY KEY);
EOF
# Validation runs before the YES prompt: with stdin closed the evil dumps
# must abort with an invalid-name error (not the "Aborted." prompt path),
# while the good dump must reach the prompt (proving validation passed).
if RESTORE_DATABASE_URL="postgres://u:p@127.0.0.1:1/postgres" \
    bash "${FIX}/scripts/restore.sh" "${T}/evil-upper.sql" </dev/null \
    >"${T}/evil-upper.log" 2>&1; then
  fail "dump with malicious uppercase database name was accepted"
else
  if grep -qi "invalid database name" "${T}/evil-upper.log" \
      && ! grep -q "Aborted" "${T}/evil-upper.log"; then
    pass "dump with malicious database name rejected before the prompt"
  else
    fail "malicious-name rejection unclear: $(cat "${T}/evil-upper.log")"
  fi
fi
if RESTORE_DATABASE_URL="postgres://u:p@127.0.0.1:1/postgres" \
    bash "${FIX}/scripts/restore.sh" "${T}/evil-lower.sql" </dev/null \
    >"${T}/evil-lower.log" 2>&1; then
  fail "dump with malicious lowercase database name was accepted"
else
  if grep -qi "invalid database name" "${T}/evil-lower.log"; then
    pass "lowercase CREATE DATABASE names are validated too"
  else
    fail "lowercase malicious name not caught: $(cat "${T}/evil-lower.log")"
  fi
fi
if RESTORE_DATABASE_URL="postgres://u:p@127.0.0.1:1/postgres" \
    bash "${FIX}/scripts/restore.sh" "${T}/good-names.sql" </dev/null \
    >"${T}/good-names.log" 2>&1; then
  fail "good-names dump unexpectedly succeeded against a dead port"
else
  if grep -q "Aborted" "${T}/good-names.log"; then
    pass "valid database names (incl. quoted) pass validation"
  else
    fail "valid dump did not reach the prompt: $(cat "${T}/good-names.log")"
  fi
fi

# ── 5b. verify-restore section filter (case-insensitive) ────────────────────────
echo "--- section filter ---"
cat > "${T}/sections.sql" <<'EOF'
CREATE ROLE rogue WITH LOGIN PASSWORD 'x';
CREATE DATABASE freecloud;
\connect freecloud
CREATE TABLE users (id SERIAL PRIMARY KEY);
create database sneaky;
CREATE DATABASE "Sneaky2";
alter database freecloud set search_path = public;
\connect keycloak
CREATE TABLE realm (id SERIAL PRIMARY KEY);
drop database if exists freecloud;
EOF
run_extract() {
  TMPDIR="${T}" FC_LIB_ONLY=1 bash -c '. "$0"; extract_section "$1" "$2" "$3"' \
    "${FIX}/scripts/verify-restore.sh" "${T}/sections.sql" "$1" "$2"
}
if run_extract freecloud "${T}/section-fc.sql" \
    && grep -q "CREATE TABLE users" "${T}/section-fc.sql" \
    && ! grep -qi "sneaky\|alter database" "${T}/section-fc.sql" \
    && ! grep -q "CREATE DATABASE" "${T}/section-fc.sql"; then
  pass "section filter keeps table DDL and drops CREATE/ALTER DATABASE in any case"
else
  fail "section filter let a cluster statement through: $(cat "${T}/section-fc.sql" 2>/dev/null)"
fi
if run_extract keycloak "${T}/section-kc.sql" \
    && grep -q "CREATE TABLE realm" "${T}/section-kc.sql" \
    && ! grep -qi "drop database" "${T}/section-kc.sql" \
    && ! grep -q "CREATE TABLE users" "${T}/section-kc.sql"; then
  pass "keycloak section extracts cleanly (no cross-section bleed)"
else
  fail "keycloak section extraction wrong: $(cat "${T}/section-kc.sql" 2>/dev/null)"
fi
if run_extract missingdb "${T}/section-empty.sql" \
    && [ ! -s "${T}/section-empty.sql" ]; then
  pass "unknown database yields an empty section"
else
  fail "unknown database yielded a non-empty section"
fi

# ── 6. Malicious secrets.tar members rejected ─────────────────────────────────
echo "--- secrets.tar member validation ---"
python3 - "${T}" <<'PY'
import sys, tarfile, io
d = sys.argv[1]
def write(name, members):
    # members: list of (arcname, kind, target?) with kind in reg/dir/sym/hard.
    with tarfile.open(f"{d}/{name}", "w") as t:
        for m in members:
            arc, kind = m[0], m[1]
            ti = tarfile.TarInfo(arc)
            if kind == "dir":
                ti.type = tarfile.DIRTYPE
                t.addfile(ti)
            elif kind == "sym":
                ti.type = tarfile.SYMTYPE
                ti.linkname = m[2]
                t.addfile(ti)
            elif kind == "hard":
                ti.type = tarfile.LNKTYPE
                ti.linkname = m[2]
                t.addfile(ti)
            else:
                data = b"secret-bytes"
                ti.size = len(data)
                t.addfile(ti, io.BytesIO(data))
write("good.tar", [(".secrets", "dir"), (".secrets/secrets.env", "reg"),
                   (".secrets/files", "dir"), (".secrets/files/x", "reg")])
write("dotdot.tar", [(".secrets", "dir"), (".secrets/../../evil", "reg")])
write("abs.tar", [(".secrets", "dir"), ("/etc/cron.d/evil", "reg")])
write("sym.tar", [(".secrets", "dir"), (".secrets/link", "sym", "/etc/passwd")])
write("hard.tar", [(".secrets", "dir"), (".secrets/hard", "hard", ".secrets/secrets.env")])
write("prefix.tar", [("otherdir/x", "reg")])
PY
if bash "${FIX}/scripts/check-secrets-tar.sh" "${T}/good.tar" >/dev/null 2>&1; then
  pass "well-formed secrets.tar accepted"
else
  fail "well-formed secrets.tar rejected"
fi
for bad in dotdot abs sym hard prefix; do
  if bash "${FIX}/scripts/check-secrets-tar.sh" "${T}/${bad}.tar" \
      >"${T}/tar-${bad}.log" 2>&1; then
    fail "malicious secrets.tar (${bad}) was accepted"
  else
    if grep -qi "unsafe\|reject\|symlink\|absolute\|\.\." "${T}/tar-${bad}.log"; then
      pass "malicious secrets.tar (${bad}) rejected with a clear message"
    else
      fail "malicious secrets.tar (${bad}) rejected without a clear message"
    fi
  fi
done

# ── 7. Legacy-format bundle: refused by default, accepted with --legacy ───────
echo "--- legacy bundle format ---"
LEGACY_DIR="${T}/legacy-plain"
rm -rf "${LEGACY_DIR}"
mkdir -p "${LEGACY_DIR}/freecloud-bundle-legacy"
cp "${T}/dump-v1.sql" "${LEGACY_DIR}/freecloud-bundle-legacy/cluster.sql"
tar -cf "${LEGACY_DIR}/freecloud-bundle-legacy/secrets.tar" -C "${FIX}" .secrets
cp "${FIX}/.env.prod" "${LEGACY_DIR}/freecloud-bundle-legacy/.env.prod"
# Old-shape manifest: no .env.prod hash, no inventory section.
{
  echo "FreeCloud backup bundle"
  echo "timestamp_utc: 20200101T000000Z"
  echo "mode: direct"
  echo "hostname: legacy-fixture"
  echo "git_head: unknown"
  echo "freecloud_version: unset"
  echo "env_prod: present"
  echo "files:"
  echo "  cluster.sql 1 bytes"
  echo "  secrets.tar 1 bytes"
  echo "  .env.prod 1 bytes"
  echo "sha256:"
  (cd "${LEGACY_DIR}/freecloud-bundle-legacy" && sha256sum cluster.sql secrets.tar)
} > "${LEGACY_DIR}/freecloud-bundle-legacy/manifest.txt"
tar -czf - -C "${LEGACY_DIR}" freecloud-bundle-legacy \
  | BACKUP_PASSPHRASE="${BACKUP_PASSPHRASE}" openssl enc -aes-256-cbc -salt -pbkdf2 \
      -pass env:BACKUP_PASSPHRASE -out "${T}/legacy.tar.gz.enc"
cp "${LEGACY_DIR}/freecloud-bundle-legacy/manifest.txt" "${T}/legacy.manifest.txt"
if BACKUP_PASSPHRASE="${BACKUP_PASSPHRASE}" \
    bash "${FIX}/scripts/verify-bundle.sh" "${T}/legacy.tar.gz.enc" "${T}/legacy-out" \
    >"${T}/legacy-refused.log" 2>&1; then
  fail "legacy bundle was accepted without --legacy"
else
  if grep -qi "legacy" "${T}/legacy-refused.log"; then
    pass "legacy bundle refused without --legacy (names the legacy flag)"
  else
    fail "legacy bundle refused without naming --legacy"
  fi
fi
if BACKUP_PASSPHRASE="${BACKUP_PASSPHRASE}" \
    bash "${FIX}/scripts/verify-bundle.sh" --legacy "${T}/legacy.tar.gz.enc" \
    "${T}/legacy-out" >"${T}/legacy-ok.log" 2>&1; then
  if grep -qi "legacy" "${T}/legacy-ok.log" && [ -f "${T}/legacy-out/cluster.sql" ]; then
    pass "legacy bundle accepted with --legacy (loud legacy warning)"
  else
    fail "legacy bundle accepted with --legacy but no warning or no output"
  fi
else
  fail "legacy bundle rejected even with --legacy: $(tail -n 3 "${T}/legacy-ok.log")"
fi

# ── 9. Safe secrets loader ────────────────────────────────────────────────────
echo "--- safe secrets loader ---"
cat > "${T}/good.env" <<'EOF'
# comment line
POSTGRES_PASSWORD=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
AUTH_SECRET=YWJjK3Zvby9iYXI9PQ==
EMPTY_OK=
_FOO=bar
EOF
if bash "${FIX}/scripts/load-secrets-env.sh" "${T}/good.env" -- \
    bash -c 'printf "%s|%s|%s|%s" "$POSTGRES_PASSWORD" "$AUTH_SECRET" "${EMPTY_OK-unset}" "$_FOO"' \
    >"${T}/loader-good.log" 2>&1; then
  if grep -q "0123456789abcdef.*|YWJjK3Zvby9iYXI9PQ==||bar" "${T}/loader-good.log"; then
    pass "safe loader exports hex/base64 values literally"
  else
    fail "safe loader exported wrong values: $(cat "${T}/loader-good.log")"
  fi
else
  fail "safe loader refused a good file: $(tail -n 2 "${T}/loader-good.log")"
fi
# The $(...) below is intentionally literal test data, not an expansion.
# shellcheck disable=SC2016
printf 'POISON=$(touch "%s/PWNED")\n' "${T}" > "${T}/cmdsub.env"
printf 'export POISON2=x\n' > "${T}/evil-export.env"
printf 'lowercase_key=x\n' > "${T}/evil-lower.env"
printf 'NOEQUALS\n' > "${T}/evil-noeq.env"
printf 'HAS SPACE=x\n' > "${T}/evil-space.env"
for badenv in evil-export evil-lower evil-noeq evil-space; do
  if bash "${FIX}/scripts/load-secrets-env.sh" "${T}/${badenv}.env" -- true \
      >"${T}/loader-${badenv}.log" 2>&1; then
    fail "safe loader accepted ${badenv}.env"
  else
    pass "safe loader refuses ${badenv}.env"
  fi
done
# POISON=$(...) matches the allowed KEY=value shape, so it must be accepted
# — but exported LITERALLY, never evaluated.
if bash "${FIX}/scripts/load-secrets-env.sh" "${T}/cmdsub.env" -- \
    bash -c 'printf "%s" "$POISON"' >"${T}/loader-cmdsub.log" 2>&1; then
  # shellcheck disable=SC2016
  if grep -qF '$(touch' "${T}/loader-cmdsub.log" && [ ! -e "${T}/PWNED" ]; then
    pass "matching \$(...) value exported literally, never evaluated"
  else
    fail "command substitution value mangled or EXECUTED"
  fi
else
  fail "safe loader refused a well-formed KEY=value line"
fi
# ── 8. Live restore: inventory match passes, mismatch fails ─────────────────────
echo "--- live restore against a throwaway postgres ---"
if [ "${HAVE_DOCKER}" -eq 0 ]; then
  skip "live restore round trip (docker not available)"
  skip "inventory mismatch fails restore (docker not available)"
  skip "bundle without inventory warns loudly (docker not available)"
else
  SCRATCH_PW="test-pw-${RANDOM}${RANDOM}"
  # The workdir is mounted read-only into the container so the psql shim
  # (docker exec) can read restore.sh's --file paths; no port is published.
  if ! docker run -d --name "${CTR}" \
      -e POSTGRES_USER=postgres -e "POSTGRES_PASSWORD=${SCRATCH_PW}" \
      -e POSTGRES_DB=postgres -v "${T}:${T}:ro" \
      "${PG_IMAGE}" >"${T}/ctr.log" 2>&1; then
    fail "could not start the throwaway postgres container"
  else
    deadline=$((SECONDS + 120))
    while ! docker exec "${CTR}" pg_isready -h 127.0.0.1 -U postgres -d postgres >/dev/null 2>&1; do
      if [ "${SECONDS}" -ge "${deadline}" ]; then
        break
      fi
      sleep 2
    done
    if ! docker exec "${CTR}" pg_isready -h 127.0.0.1 -U postgres -d postgres >/dev/null 2>&1; then
      fail "throwaway postgres never became ready; container log:"
      docker logs "${CTR}" 2>&1 | tail -n 15 || true
      skip "live restore round trip (scratch postgres not ready)"
      skip "inventory mismatch fails restore (scratch postgres not ready)"
      skip "bundle without inventory warns loudly (scratch postgres not ready)"
      docker rm -f "${CTR}" >/dev/null 2>&1 || true
    else
    # psql shim: restore.sh's psql calls run inside the container, which
    # listens on its own loopback — no published port needed.
    cat > "${FIX}/bin/psql" <<EOF
#!/usr/bin/env bash
exec docker exec -i -e PGPASSWORD="\${PGPASSWORD:-}" "${CTR}" psql "\$@"
EOF
    chmod +x "${FIX}/bin/psql"
    export RESTORE_DATABASE_URL="postgres://postgres:${SCRATCH_PW}@127.0.0.1:5432/postgres"

    # Positive control: restorable dump, intact manifest -> exit 0.
    FC_CANNED_DUMP="${T}/dump-v2.sql" BACKUP_PASSPHRASE='' \
      bash "${FIX}/scripts/backup-bundle.sh" --allow-plaintext "${FIX}/backups-v2" \
      >"${T}/bundle-v2.log" 2>&1
    V2_BUNDLE="$(find "${FIX}/backups-v2" -maxdepth 1 -name 'freecloud-bundle-*' | sort | tail -n 1)"
    if [ -z "${V2_BUNDLE}" ]; then
      fail "could not build the v2 fixture bundle"
    elif printf 'YES\n' | RESTORE_DATABASE_URL="${RESTORE_DATABASE_URL}" \
        PATH="${FIX}/bin:${PATH}" bash "${FIX}/scripts/restore.sh" \
        --bundle "${V2_BUNDLE}" >"${T}/restore-v2.log" 2>&1; then
      if grep -qi "inventory.*\(ok\|match\)" "${T}/restore-v2.log"; then
        pass "intact bundle restores with inventory OK"
      else
        fail "restore passed but printed no inventory confirmation"
      fi
    else
      fail "intact bundle restore failed: $(tail -n 5 "${T}/restore-v2.log")"
    fi

    # Inventory mismatch: dump text counts a table that fails at restore time.
    FC_CANNED_DUMP="${T}/dump-v3.sql" BACKUP_PASSPHRASE='' \
      bash "${FIX}/scripts/backup-bundle.sh" --allow-plaintext "${FIX}/backups-v3" \
      >"${T}/bundle-v3.log" 2>&1
    V3_BUNDLE="$(find "${FIX}/backups-v3" -maxdepth 1 -name 'freecloud-bundle-*' | sort | tail -n 1)"
    if [ -z "${V3_BUNDLE}" ]; then
      fail "could not build the v3 fixture bundle"
    elif printf 'YES\n' | RESTORE_DATABASE_URL="${RESTORE_DATABASE_URL}" \
        PATH="${FIX}/bin:${PATH}" bash "${FIX}/scripts/restore.sh" \
        --bundle "${V3_BUNDLE}" >"${T}/restore-v3.log" 2>&1; then
      fail "restore with inventory mismatch unexpectedly passed"
    else
      if grep -qi "mismatch" "${T}/restore-v3.log"; then
        pass "restore with inventory mismatch fails loudly"
      else
        fail "inventory-mismatch restore failed without a mismatch message"
      fi
    fi

    # Old bundle without an inventory section: warns loudly, does not
    # silently pass.
    NOINV_DIR="${T}/noinv"
    rm -rf "${NOINV_DIR}"
    cp -r "${V2_BUNDLE}" "${NOINV_DIR}"
    chmod -R u+rw "${NOINV_DIR}"
    sed -i '/^inventory:$/,$d' "${NOINV_DIR}/manifest.txt"
    if printf 'YES\n' | RESTORE_DATABASE_URL="${RESTORE_DATABASE_URL}" \
        PATH="${FIX}/bin:${PATH}" bash "${FIX}/scripts/restore.sh" \
        --bundle "${NOINV_DIR}" >"${T}/restore-noinv.log" 2>&1; then
      if grep -qi "warning.*inventory\|inventory.*warning\|no inventory" \
          "${T}/restore-noinv.log"; then
        pass "bundle without inventory restores with a loud warning"
      else
        fail "bundle without inventory passed SILENTLY"
      fi
    else
      fail "bundle without inventory unexpectedly failed: $(tail -n 3 "${T}/restore-noinv.log")"
    fi
    fi
    docker rm -f "${CTR}" >/dev/null 2>&1 || true
  fi
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "test-backup-integrity: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped"
if [ "${FAIL}" -ne 0 ]; then
  exit 1
fi
exit 0
