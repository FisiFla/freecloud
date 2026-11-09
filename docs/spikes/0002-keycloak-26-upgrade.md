# Spike 0002 — Coordinated Keycloak 26 upgrade assessment

**Date:** 2026-11-08 · **Branch:** `spike/keycloak-26` (spike only — do not merge as-is)
**Question:** What does the deferred Keycloak 25 → 26 coordinated upgrade actually cost?
**Verdict: GO** (effort **M** — small diff, medium validation). The feared SPI/Jakarta break does not exist for this codebase: the authenticator compiles, tests, shades, builds into the image, boots, and registers under Keycloak 26.8.2 with zero Java source changes. The real work is env-var renames, the in-place DB schema upgrade path, and e2e validation of the backend bootstrap against the 26 Admin API.

Deferred context (`.ai_infinite_backlog.md:14`): PRs #230 (jakarta.ws.rs-api 3.1.0→4.0.0), #235 (image 25.0→26.8), #238 (keycloak.version 25.0.0→26.8.1) were closed as belonging to this coordinated upgrade. This spike replays exactly that trio, at the now-latest 26 patch.

## 1. Every Keycloak 25 pin (file:line)

Functional pins — the complete coordinated set:

| Pin | Location | Value |
|---|---|---|
| SPI compile version | `keycloak-authenticator/pom.xml:16` (`keycloak.version`, consumed at :24, :30, :36) | `25.0.0` |
| Jakarta WS API | `keycloak-authenticator/pom.xml:42` | `3.1.0` (deferred PR #230 → 4.0.0) |
| Runtime image | `docker/Dockerfile.keycloak:28` | `quay.io/keycloak/keycloak:25.0@sha256:82c5b7a1…` |

Everything else is downstream of those three:

- **No compose file pins a Keycloak version.** All four stacks build `docker/Dockerfile.keycloak` (`docker/docker-compose.yml:26`, `docker-compose.prod.yml:83`, `docker-compose.localhost.yml:70`, `docker-compose.e2e.yml:29`). One image pin to move.
- **Builder/CI Java** is 17 (`docker/Dockerfile.keycloak:14` maven temurin-17, `.github/workflows/verify.yml:29`, `.github/workflows/e2e.yml:32`) — no change needed (see §3).
- **Backend Admin client** `github.com/Nerzal/gocloak/v13 v13.9.0` (`backend/go.mod:6`) speaks REST; it carries no KC-25 version pin.
- Cosmetic/doc pins: `docker/Dockerfile.keycloak:5` (comment), `README.md:10` ("Keycloak 25+"), `docs/ARCHITECTURE.md:25`, `docs/DEPLOYMENT.md:298` (digest-refresh example), `docs/spikes/0001-conditional-access-authenticator.md:160,219` (historical — leave).
- False positive: `docker/docker-compose.observability.yml:44` is blackbox-exporter `v0.25.0`, unrelated.
- The SPI is **already Jakarta** (`jakarta.json.*`, `jakarta.ws.rs.*` in `PostureCheckAuthenticator.java:3-8`); there is no `javax.*` anywhere. "Jakarta API move" in the backlog note = the `jakarta.ws.rs-api` 3.1→4.0 bump only.

Version-sensitive *behaviour* surface (not version pins, but moves with the upgrade):

- Legacy bootstrap-admin envs `KEYCLOAK_ADMIN` / `KEYCLOAK_ADMIN_PASSWORD`: `docker/docker-compose.yml:28-29`, `docker-compose.localhost.yml:77-78`, `docker-compose.e2e.yml:31-32`, `docker-compose.prod.yml:91`, exported by `docker/keycloak-entrypoint.sh:27`. Deprecated in 26 (see §3).
- `KC_HOSTNAME_PORT`: `docker/docker-compose.localhost.yml:85`, `docker/docker-compose.yml:36` — under KC 26's hostname-v2 defaults this needs a live check (not exercised in this spike).
- `KC_HOSTNAME` full-URL form (`docker-compose.e2e.yml:49`, prod `KC_HOSTNAME: ${KC_PUBLIC_URL}`) already matches hostname-v2 expectations.
- Login-theme template `keycloak-authenticator/src/main/resources/theme-resources/templates/access-blocked.ftl` uses `registrationLayout` from the stock login theme; KC 26 ships both `keycloak` and `keycloak.v2` login themes (confirmed via serverinfo, §3) — rendering must be eyeballed in the real PR.
- SPI registration file `META-INF/services/org.keycloak.authentication.AuthenticatorFactory` — unchanged mechanism in 26.

## 2. Bumps made in this spike

Target selection (2026-11-08): Maven Central metadata for `org.keycloak:keycloak-core` ends at **26.8.2** (lastUpdated 2026-11-05); quay tags `26.8` and `26.8.2` resolve to the same manifest `sha256:3879c623248408de29a5303178b5816c77e8dfef3890c361f8c0bbeaa979ba3b`.

1. `keycloak-authenticator/pom.xml:16` — `keycloak.version` `25.0.0` → `26.8.2`
2. `keycloak-authenticator/pom.xml:42` — `jakarta.ws.rs-api` `3.1.0` → `4.0.0` (the deferred #230)
3. `docker/Dockerfile.keycloak:28` — image `25.0@sha256:82c5…` → `26.8@sha256:3879c623…`; comment at :5 updated 25 → 26

No Java source, compose, CI, or Go changes were made in the spike.

## 3. Exact build / test outcome

Environment caveat: no JDK 17 on this host and the Docker daemon was initially down. Unit tests ran on brew OpenJDK 25.0.5 + Maven 3.10.0 (target remains 17); Java-17 compatibility was verified separately by bytecode scan (below). Colima was started for the image/runtime checks.

- **Baseline (25.0.0), `mvn test`:** `Tests run: 24, Failures: 0, Errors: 0, Skipped: 0` / `BUILD SUCCESS` — matches the backlog's recorded 24/24.
- **Bumped (26.8.2 + jakarta 4.0.0), `mvn clean package`** (clean, so a real recompile): `Compiling 5 source files`, `Tests run: 24, Failures: 0, Errors: 0, Skipped: 0`, shade completes (171K fat jar), `BUILD SUCCESS`. **First real failure: none.** No deprecation warnings surfaced under `-Xlint:deprecation` either. (An earlier non-clean run also passed but is not cited — incremental compile said "Nothing to compile".)
- **Bytecode / Java-17 check:** every class in `keycloak-core`, `keycloak-server-spi`, `keycloak-server-spi-private`, `keycloak-common` 26.8.2 and `jakarta.ws.rs-api` 4.0.0 is major version ≤ 61 (Java 17). The temurin-17 builder and CI `java-version: '17'` need no change. The 26.8.2 *image* itself runs JVM 21.0.13 internally — irrelevant to our bytecode.
- **Image build** `docker build -f docker/Dockerfile.keycloak .`: succeeds end to end. `kc.sh build` discovers the provider — verbatim: `KC-SERVICES0047: freecloud-posture-check (xyz.fisicaro.freecloud.keycloak.PostureCheckAuthenticatorFactory) is implementing the internal SPI authenticator.` (pre-existing class of warning: the Authenticator SPI is "internal", same posture as on 25) and `Quarkus augmentation completed`.
- **Runtime smoke:** image + throwaway Postgres 16, `start-dev`, legacy `KEYCLOAK_ADMIN*` envs. Verbatim key lines:
  - `KC-SERVICES0110: Environment variable 'KEYCLOAK_ADMIN' is deprecated, use 'KC_BOOTSTRAP_ADMIN_USERNAME' instead` (same for `KEYCLOAK_ADMIN_PASSWORD` → `KC_BOOTSTRAP_ADMIN_PASSWORD`) — **deprecated but still honoured in 26.8.2**: `Created temporary admin user with username admin`, `Keycloak 26.8.2 … started in 12.108s`.
  - `GET /realms/master` → 200; password grant against `admin-cli` issues a token; `GET /admin/serverinfo` lists `freecloud-posture-check` under authenticator providers; login themes present: `['keycloak', 'keycloak.v2']`.
  - A first smoke attempt without a database failed with `ERROR: Failed to obtain JDBC connection … Connection to localhost:5432 refused` — expected (`KC_DB=postgres` is baked in; no DB was provided), **not** a 26 regression. Our factory had already initialised by then (its `SECURITY: device posture enforcement is DISABLED …` warning logged).

Not exercised by this spike (real-PR validation list): full compose e2e (backend bootstrap + posture flow + HA), `KC_HOSTNAME_PORT` stacks, `access-blocked.ftl` rendering, and — most importantly — boot of 26 against an **existing 25 database** (next section).

## 4. Realm / export migration implications

**There is no realm export to migrate.** No realm JSON/YAML exists in the repo (`find -name '*realm*'` → only `docs/adr/0005-multi-tenant-shared-realm.md`); nothing uses `--import-realm` or `kc.sh export/import`. The realm is created and kept converged by the backend at runtime via the Admin REST API (`backend/internal/bootstrap/bootstrap.go:230` `CreateRealm`, idempotent ensure-steps under a pg advisory lock, `backend/cmd/server/main.go:160-227`). So "realm migration" decomposes into:

1. **Keycloak's own DB schema upgrade** (Liquibase) runs in place on first 26 boot against the existing `keycloak` Postgres database. This is **one-way**: a 25 image cannot run against a 26-migrated schema. Rollback = restore the pre-upgrade dump + old image. `docs/BACKUP_RESTORE.md:14-17,155-162` already treats the `keycloak` DB as part of the cluster dump — take that dump immediately before the upgrade deploy.
2. **Bootstrap idempotency against the 26 Admin API.** On the upgraded realm the backend re-runs its ensure-* path (groups, clients, secret stability, posture-flow execution ordering, token-lifespan backfill — covered by `backend/internal/bootstrap/bootstrap_test.go`). gocloak v13.9.0's representations are broadly stable across 25→26, but this is the one place a silent behavioural drift (e.g. user-profile defaults, flow-copy semantics) could bite; it is exactly what `docker-compose.e2e.yml` + the turnkey/HA e2e tests exist to catch.
3. **Fresh installs** are unaffected structurally: 26 creates the schema, backend creates the realm — the same code path, already smoke-proven for master-realm boot above.

## 5. Ordered migration steps for the real PR

1. Apply the three spike bumps (pom ×2, Dockerfile image + digest). Take the digest fresh at PR time per `docs/DEPLOYMENT.md:298` (update that example to the 26 tag in the same PR).
2. Rename bootstrap-admin envs to `KC_BOOTSTRAP_ADMIN_USERNAME` / `KC_BOOTSTRAP_ADMIN_PASSWORD` in all four compose files and `docker/keycloak-entrypoint.sh:27`. Mind the entrypoint's existing warning: `KC_*` names become CLI options — these two are the documented 26 names (per KC-SERVICES0110), but verify prod secrets-file flow still works. Do **not** rename the backend's own `KC_BOOTSTRAP_ADMIN` / `KC_BOOTSTRAP_PASSWORD` config — those are backend-process names, unrelated to Keycloak's.
3. Verify `KC_HOSTNAME_PORT` stacks (`docker-compose.yml`, `docker-compose.localhost.yml`) under hostname v2; switch to URL-form `KC_HOSTNAME` if the port variant misbehaves.
4. `cd keycloak-authenticator && mvn test` (CI verify does this) and `make kc-build`.
5. Full e2e: `docker-compose.e2e.yml` suite (turnkey bootstrap, posture-check flow incl. the `access-blocked.ftl` page render, client-IP proxy case) and the HA variant.
6. **Upgrade-path test (the step fresh-DB e2e cannot cover):** stand up the 25 stack, seed realm/users via backend bootstrap, dump the DB, swap in the 26 image, boot, confirm schema migration completes, realm/users/clients intact, logins + posture flow work, backend bootstrap second run is a clean no-op. Document the operator runbook: dump → swap image → boot → watch for migration completion; rollback = restore dump + 25 image.
7. Sweep docs: `README.md:10`, `docs/ARCHITECTURE.md:25` (25+ → 26+), `docs/DEPLOYMENT.md:298`. Leave `docs/spikes/0001…` as history.
8. Close the loop on the deferred dependabot trio (#230/#235/#238) in the PR description and refresh the backlog line.

## 6. Effort & recommendation

**Effort: M.** The diff itself is S (three functional lines + env renames + docs). Validation dominates: e2e suite, hostname check, theme render, and one deliberate 25→26 in-place DB upgrade rehearsal.

**Recommendation: GO.** Every compile-time and packaging fear behind the deferral (SPI written against 25.0, Jakarta move, Java version) is empirically absent: 24/24 tests, clean shade, clean `kc.sh build`, clean boot, provider registered — with zero Java changes. Schedule it as its own PR (not bundled with other upgrades), gated on steps 5–6 above. Remaining risk is concentrated where it should be: the one-way DB schema migration and Admin-API behaviour of the Go bootstrap — both testable before merge, neither requiring code archaeology.

## Addendum (PR #266 validation): the `kc-admin-api` audience requirement

Step 5's e2e validation surfaced one real Admin-API behavioural break, exactly the class §4.2 warned about. Since **Keycloak 26.8.2** the Admin REST API only accepts tokens carrying the audience added by the built-in **`kc-admin-api`** client scope (`<kc>/admin/realms/<realm>` in the token `aud` claim). The `freecloud-service` client created/converged by `backend/internal/bootstrap/bootstrap.go` never had that scope, so on 26.8.2 every *runtime* Admin call by the backend failed with **401** (observed in e2e: create app/user, setup status). Bootstrap itself was unaffected (it authenticates as the master-realm admin).

Fix (this PR, verified live against KC 26.8.2 before implementation): assigning `kc-admin-api` as a **default** client scope of `freecloud-service` made the same Admin calls return **200**, with the token `aud` gaining `<kc>/admin/realms/<realm>`. Bootstrap now does this idempotently on every run (`ensureServiceClientAdminAPIScope`): it looks the scope up by name and assigns it only when present, so Keycloak < 26.8.2 — where the scope does not exist — is a no-op and KC 25 keeps working unchanged. Covered by `TestBootstrap_KCAdminAPIScope_AssignedAsDefaultWhenPresent` and `TestBootstrap_KCAdminAPIScope_AbsentIsNoOp` in `backend/internal/bootstrap/bootstrap_test.go`.

## Addendum (2026-11-09 rehearsal): in-place 25 → 26 upgrade on an existing 25 database

The one validation §3/§5 left open — boot of 26 against an **existing 25 database** — has now been rehearsed end-to-end. The rehearsal is shipped as a reusable, self-contained script: `scripts/rehearse-kc-upgrade.sh` (own Docker network/volume/containers, tears down on exit unless `KEEP=1`, non-zero exit on any failed check, prints one `RESULT PASS|FAIL <item>` line per check). Run on 2026-11-09 (Docker 29): **exit 0, all 11 checks passed.**

**Method.** The pre-upgrade 25 setup was taken from git history, not approximated: the 25 image was built from a scratch worktree at `ac2ebbe^` (parent of the #266 upgrade commit), i.e. `quay.io/keycloak/keycloak:25.0@sha256:82c5b7a1…` + the SPI compiled against `keycloak.version 25.0.0` (jakarta.ws.rs-api 3.1.0), booted with the pre-rename `KEYCLOAK_ADMIN` / `KEYCLOAK_ADMIN_PASSWORD` envs — the SPI and `access-blocked.ftl` were therefore present in the 25 boot exactly as in production (25 reported version `25.0.6`). It was seeded via the Admin REST API to mirror `backend/internal/bootstrap/bootstrap.go`: realm `freecloud` (same flags, 300 s token lifespan), realm role `admin`, the four department groups, `freecloud-service` (confidential, service account, full scope, `realm-admin` grant), `freecloud-dashboard` (public), the `browser-with-posture` flow copied from `browser` with the `freecloud-posture-check` execution REQUIRED inside the forms sub-flow and bound as the realm browser flow, and two users with password credentials (alice in Engineering + holding the `admin` role, bob). Pre-upgrade ids/counts were snapshotted, 25 was stopped, a `pg_dump` was taken and verified non-empty (302,593 bytes — the script refuses to proceed without it, per the one-way-migration warning in §4), and the current 26.8.2 image (built from HEAD `docker/Dockerfile.keycloak`) was booted against the same database volume.

**Result per previously-unproven item:**

- **In-place Liquibase upgrade — PROVEN.** First 26 boot logged `Updating database. Using changelog META-INF/jpa-changelog-master.xml` followed by the model migrations (`Migrating older model to 26.0.0` … through 26.6.1, with `migrated realm freecloud to …` per step) and started cleanly. `DATABASECHANGELOG` grew 134 → 238 rows.
- **Data survival — PROVEN.** Realm id, all seeded client UUIDs, both user ids (service-account user present throughout), all four group ids, user count, browser-flow binding, and the posture execution inside the migrated flow were byte-identical pre/post. A password-grant token for seeded user alice succeeded post-upgrade (`sub` = her pre-upgrade id), so the migrated password credential verifies under 26.
- **SPI on the migrated DB — PROVEN.** `freecloud-posture-check` is registered in `serverinfo` on 26 and still present as an execution in the migrated `browser-with-posture` flow.
- **`kc-admin-api` audience on a MIGRATED database — PROVEN (this is the cycle-22 catch, now on the upgrade path, not just fresh installs).** The migrated 25 realm gained the `kc-admin-api` client scope automatically during the upgrade (absent on 25, `kc_admin_api_scope: false` → present on 26). Before assigning it, a `freecloud-service` service-account Admin call returned **401** (token `aud`: `["realm-management", "account"]`); after assigning `kc-admin-api` as a default client scope — exactly what `ensureServiceClientAdminAPIScope` does at bootstrap — the same call returned **200** with `aud` gaining `<kc>/admin/realms/freecloud`. Upgraded deployments therefore still need one bootstrap run of the cycle-22+ backend before runtime Admin calls work, and that run does fix a migrated realm.
- **`access-blocked.ftl` under the 26 login theme — PROVEN (rendered, not just eyeballed).** The provider jar ships `theme-resources/templates/access-blocked.ftl`; in the 26 themes jar `template.ftl` defining `registrationLayout` exists in `base` (the declared parent of both the `keycloak` and `keycloak.v2` login themes) and in `keycloak.v2` itself. A real browser-flow login (posture enabled, access-eval stub returning `allow:false` with reason `rehearsal-deny`) rendered the access-blocked page — body text `Access denied: your device does not meet posture requirements` and the `rehearsal-deny` reason — under the **keycloak.v2** login theme (resource paths `/resources/<ver>/login/keycloak.v2/…`). The only cosmetic caveat: the page title comes out as the `accessBlocked` message key, which no message bundle in the jar defines, so the title/header is not a translated string (the page `<title>` was the default `Sign in to FreeCloud`); the blocking message itself is hard-coded English in the template and renders regardless.
- **`KC_HOSTNAME_PORT` stacks — PROVEN with a caveat.** Rebooting the migrated 26 with `KC_HOSTNAME=localhost` + `KC_HOSTNAME_PORT=18081` (the `docker-compose.yml` / `docker-compose.localhost.yml` shape) worked: `.well-known` issuer was `http://localhost:18081/realms/freecloud` and a password grant still returned 200 with that issuer. Caveat: 26.8.2 logs `WARNING: Hostname v1 options [hostname-port] are still in use, please review your configuration` — hostname-port is a deprecated v1 option the compose stacks should eventually replace with the URL form of `KC_HOSTNAME`, but it is honoured today.

**Fidelity limits (what this rehearsal is not).** (1) The 25 database is synthesized fresh by KC 25 itself and seeded via Admin REST mirroring bootstrap — it is not a dump of a long-lived production realm, so production-only rows (offline sessions, login events, federated users, brute-force state) were not carried through the migration. (2) Seeding diverges from bootstrap in two documented ways, both only to make behaviour checkable: `freecloud-dashboard` got a localhost redirect URI, and an extra public `rehearsal-direct-grant` client exists for the password-grant check (bootstrap deliberately disables direct grants on its own clients). (3) The backend binary itself never ran — bootstrap's post-upgrade convergence against the 26 Admin API is covered by CI e2e on fresh DBs plus the scope-assignment check above, not re-run here against the migrated DB. (4) One database, one realm, `start-dev` mode, no HA/clustering, no TLS/hostname-strict production mode. (5) Rollback (restore the pre-upgrade dump + 25 image) was prepared (dump verified non-empty, kept as a rehearsal artefact) but the restore itself was not executed.
