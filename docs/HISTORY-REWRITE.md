# History rewrite — commit attribution

**Date:** 2026-10-22
**Reason:** 25 commits in this repository carried an author/committer email that GitHub
resolved to a *different* account (`Fisicaro`), so the repository's contributor graph
showed work split across two accounts. The commits themselves were correct — the push
authentication, the author *name* and every tree were the canonical `FisiFla` identity —
but GitHub attributes a commit to the account that owns the **email in the commit
header**, not to whoever pushed it.

**What changed:** every non-dependabot commit was rewritten so that its author and
committer are both `FisiFla <flavio@fisicaro.xyz>`. The 44 `dependabot[bot]` commits were
left untouched. No file content changed — every tree is byte-identical (the tree SHA of
`8e8e81e` and `66a37f5` are the same).

**Scale:** 275 commits re-created, 25 kept their original hash (the
oldest ones, which already carried the canonical identity). The branch was force-pushed.

**Old HEAD:** `8e8e81ee0cd1e2c39deb7b105096c7b319a6f35c`
**New HEAD:** `66a37f5296a526732221e7cbbf77f8549d11e99f`

**Recovery:** the pre-rewrite history is preserved in a local bundle. From a clone of
this repository, `git fetch /path/to/freecloud-pre-rewrite.bundle 'refs/heads/main:refs/backup/old-main'`
brings the old commits back.

**Other clones:** any working copy must be re-synced with
`git fetch origin && git reset --hard origin/main`. Local branches that were never
pushed are unaffected.

## Commit map (old → new)

| old | new | subject |
|---|---|---|
| `457be36` | `9447e80` | fix: address code review findings (security, correctness, polish) |
| `f95b040` | `7bb763b` | Merge pull request #1 from FisiFla/fix/code-review-fixes |
| `ed322f0` | `8534071` | feat: migrate frontend auth from next-auth v4 to Auth.js v5 |
| `1edfa12` | `664a795` | Merge pull request #2 from FisiFla/feat/authjs-v5 |
| `4241c34` | `2000a83` | hardening: security, bug fixes, and QoL improvements |
| `75d589d` | `59caded` | fix: rate-limit identity, test coverage, edge runtime warning |
| `beb7bde` | `b87c774` | polish: deprovision UX, test depth, dev-safety guard, quality gate |
| `100a793` | `2a41a9c` | test+ux: DB integration harness, reusable UI states, auth context |
| `79fffbb` | `0e34f83` | fix: test-db escaping, adopt useApiReady, document gates |
| `a153d40` | `ddff2f7` | test+config: test-db preflight, verify-all honesty, env validation, frontend tests, dev-secret hardening |
| `7e0505b` | `a37cc04` | Track disabled users explicitly |
| `dddb7fa` | `9a99eff` | harden: fail-closed config validation, security headers, least-privilege SA (FCPRD-1) (#3) |
| `d3c0788` | `a123114` | fix: distributed-state integrity for onboard/offboard + Keycloak token caching (FCPRD-2) (#4) |
| `8c6607a` | `3130a01` | feat: real device-enrollment loop so the offboarding panic button wipes (FCPRD-3) (#5) |
| `9ba86eb` | `91fb2d3` | build: production Docker images + TLS Compose stack (FCPRD-4) (#6) |
| `45c55b2` | `1977be9` | ci+feat: expand CI gate, add observability endpoints, bump deps (FCPRD-5) (#7) |
| `3b82e52` | `48ac0e1` | feat: frontend production polish — error boundaries, pagination, validation (FCPRD-6) (#25) |
| `b0905ed` | `ef325b5` | docs: OSS release & ops docs — SECURITY, CONTRIBUTING, deployment, ADRs (FCPRD-7) (#26) |
| `ebb64da` | `a4cc91a` | build(deps): bump docker/build-push-action from 6 to 7 (#9) |
| `f3a36ef` | `8123f31` | build(deps): bump actions/checkout from 4 to 6 (#10) |
| `0f5e6e3` | `3d8c87c` | build(deps): bump golang from 1.25-alpine to 1.26-alpine in /backend (#11) |
| `398277b` | `2807276` | build(deps): bump github.com/go-chi/cors from 1.2.1 to 1.2.2 in /backend (#13) |
| `90f7b7d` | `815a94c` | build(deps): bump actions/setup-node from 4 to 6 (#14) |
| `edad24b` | `f5d3210` | build(deps): bump go.uber.org/zap from 1.27.0 to 1.28.0 in /backend (#17) |
| `5b3b27b` | `9e6d495` | build(deps): bump github.com/jackc/pgx/v5 in /backend (#18) |
| `b0071f1` | `f3d38ad` | build(deps-dev): bump jsdom from 25.0.1 to 29.1.1 in /frontend (#19) |
| `573d461` | `82003d8` | build(deps-dev): bump @types/node from 20.19.43 to 25.9.3 in /frontend (#20) |
| `36eb572` | `e06b19b` | build(deps): bump docker/setup-buildx-action from 3 to 4 (#12) |
| `2f5882c` | `9f87c8a` | build(deps): bump github.com/go-chi/chi/v5 in /backend (#15) |
| `8a528b9` | `41b8fda` | build(deps): bump actions/setup-go from 5 to 6 (#16) |
| `17a7a08` | `5c99a90` | chore(frontend): upgrade to TypeScript 6 + Tailwind CSS 4 (#27) |
| `a03e651` | `74e6c17` | chore(frontend): upgrade to Next.js 16 + Node 26 base image (#28) |
| `a7447de` | `4b50bf9` | Harden enrollment and production setup |
| `dae9741` | `be380c3` | build(deps): bump docker/metadata-action from 5 to 6 (#29) |
| `1cd531b` | `d9aa14c` | build(deps): bump docker/login-action from 3 to 4 (#30) |
| `6df73b9` | `cae7744` | build(deps): bump lucide-react from 0.400.0 to 1.18.0 in /frontend (#31) |
| `3b506c6` | `997f065` | build(deps-dev): bump @vitejs/plugin-react in /frontend (#32) |
| `814ed19` | `bcc376c` | chore(deps): complete React 19 upgrade (#33) + patch undici advisory (#35) |
| `cd03212` | `11450cd` | docs: drop "JumpCloud" references now that the repo is public |
| `ab1d499` | `8c99993` | Epic B: Device/MDM depth (FCEXP-13..16) (#36) |
| `9e604b3` | `77a8ea1` | Epic C: Admin UX & scale-of-use (FCEXP-17..20) (#37) |
| `c4a90f6` | `90181c0` | Epic D: Operability & ops hardening (FCEXP-21..24) (#38) |
| `ceef395` | `dcee298` | feat(epic-a): SAML app creation, SCIM 2.0 Users, group/role mgmt, user edit, password reset (#39) |
| `89b800a` | `7401386` | v1.2 Epic B: SCIM Groups, Fleet team policies, e2e harness (FCEX2-9..11) (#42) |
| `aced486` | `1001a89` | v1.2 Epic A: Conditional Access (FCEX2-5..8) (#40) |
| `a6a3323` | `5863c83` | v1.2 Epic D: Notifications, reporting & integrations (FCEX2-17..19) (#41) |
| `e4f95e9` | `5f5038a` | v1.2 Epic C: Access governance & self-service (FCEX2-12..15) (#43) |
| `ace0f8c` | `9d06dfc` | Fix RBAC enforcement and access workflows |
| `7cc28e6` | `5d34098` | Fix staticcheck in auth tests |
| `29e6799` | `7f6cafe` | Harden auth and deployment defaults |
| `a45b56f` | `2ae399c` | Fix E2E Keycloak bootstrap |
| `d14bf12` | `83ba50b` | Set Keycloak username on user create |
| `4c5a0bb` | `3101339` | Fix Go cache path in workflows |
| `44d4cf7` | `ca800ce` | test(rbac): guard that every API route is permission-gated or allowlisted |
| `a717977` | `889fddd` | v1.3 Epic B: IdP interop + app catalog (FCEX3-9..12) (#45) |
| `e8ee221` | `e4231b1` | v1.3 Epic C: Enterprise/security maturity (FCEX3-13..16) (#46) |
| `6c27802` | `0a69713` | v1.3 Epic A: Enforce conditional access (Keycloak authenticator) (FCEX3-5..8) (#44) |
| `4880749` | `1b2b0e9` | v1.3 Epic D: Adoption — UX polish + OSS docs (FCEX3-17..19) (#47) |
| `8b890e4` | `9feec01` | Harden approval and audit flows |
| `5055544` | `42ac939` | Harden audit and posture policy flows |
| `3671cc3` | `1f17a72` | Fix Keycloak SPI context test |
| `b10aec4` | `5ff2dfc` | Add SPI test JAX-RS runtime |
| `ec4b949` | `1dfdbe0` | Hardening review fixes (post-v1.3.0): self-approval, audit backfill, require_enrolled +11 (#52) |
| `acf5bf9` | `e510f8d` | build(deps): bump actions/checkout from 6 to 7 (#49) |
| `4cd6484` | `9dee1a8` | build(deps): bump actions/setup-java from 4 to 5 (#48) |
| `55b2d77` | `ade7064` | build(deps-dev): bump @types/node from 25.9.3 to 26.0.0 in /frontend (#51) |
| `cfd86bc` | `a56b299` | build(deps): bump lucide-react from 1.18.0 to 1.21.0 in /frontend (#50) |
| `e7c4064` | `d4d5fd4` | feat(v1.4): Epic D — password & account policy API + UI (FCEX4-19,20) (#53) |
| `c5f7ff6` | `384a027` | feat(v1.4): Epic E — device management depth / MDM (FCEX4-21,22,23) (#54) |
| `66c9605` | `50add33` | feat(v1.4): Epic B — MFA self-service enrollment (FCEX4-13,14,15) (#55) |
| `30029a0` | `1a21768` | feat(v1.4): Epic A — outbound provisioning SCIM→SaaS (FCEX4-9,10,11,12) (#56) |
| `c6d460c` | `85d8074` | feat(v1.4): Epic C — LDAP/AD federation (C1 backend + C2 UI + C3 guardrails) (#57) |
| `c6236b3` | `cbd8536` | feat(v1.4): Epic F — integrations hub + e2e (SCIM-out/LDAP) + parity docs (FCEX4-24,25,26) (#58) |
| `e497b82` | `697138b` | feat(v1.5): Epic A — trustworthy metrics & analytics (FCEX5-8,9,10) (#59) |
| `b02b7f2` | `255cce5` | feat(v1.5): Epic B — audit & reporting completeness (FCEX5-11,12,13) (#60) |
| `eab6ad3` | `a53e12a` | feat(v1.5): Epic C — SAML SSO completeness (FCEX5-14,15,16) (#61) |
| `2a33e86` | `56d404c` | feat(v1.5): Epic D — conditional-access policy depth (FCEX5-17,18,19) (#62) |
| `22df4ad` | `26bc6f8` | feat(v1.5): Epic E — provisioning & lifecycle depth (FCEX5-20,21,22) (#63) |
| `76c0961` | `a953b50` | feat(v1.5): Epic F — verify & document (FCEX5-23,24) (#64) |
| `73fdb81` | `0c0b48c` | feat(v1.6): Epic A — self-bootstrap Keycloak [foundational] (FCEX6-7,8,9) (#65) |
| `08dc452` | `a32d43f` | feat(v1.6): Epic B — first-run setup wizard (FCEX6-10,11,12) (#66) |
| `833488b` | `4848c82` | feat(v1.6): Epic C — turnkey packaging + auto-wired secrets (FCEX6-13,14,15) (#67) |
| `fe974b7` | `4acde6c` | feat(v1.6): Epic D — Fleet/SMTP/IdP management in-app (FCEX6-16,17,18) (#68) |
| `e54890e` | `6013aa9` | feat(v1.6): Epic E — turnkey e2e + docs rewrite (FCEX6-19,20) (#69) |
| `1482aa0` | `7a29ba9` | Harden production runtime configuration |
| `bea6622` | `c2cd9ee` | build(deps-dev): bump @types/node from 26.0.0 to 26.0.1 in /frontend (#70) |
| `4dd1df3` | `14c6df0` | build(deps-dev): bump postcss from 8.5.15 to 8.5.16 in /frontend (#71) |
| `989cbc0` | `f652c60` | build(deps): bump lucide-react from 1.21.0 to 1.22.0 in /frontend (#72) |
| `3739bb3` | `21b36a9` | build(deps-dev): bump vite from 8.0.16 to 8.1.0 in /frontend (#73) |
| `c92aa80` | `c9f3103` | build(deps-dev): bump @vitejs/plugin-react in /frontend (#74) |
| `ffb76f5` | `b9ede92` | feat(v1.7): Epic B — HA / multi-instance readiness (FCEX7-10..13) (#75) |
| `9e549e5` | `b02ce94` | feat(v1.7): Epic A — PARITY gap closure (FCEX7-5..8) (#76) |
| `ab638dd` | `bbee0eb` | feat(v1.7): Epic C — multi-tenant foundations, shared realm + org_id (FCEX7-15..19) (#77) |
| `242da4c` | `92d58ee` | docs(v1.7): Epic D — PARITY rewrite + HA/tenancy architecture and deployment docs (FCEX7-21,22) |
| `4a07147` | `f7b7b97` | fix(security): Epic A — multi-tenant isolation (C1, C2, H1, H4, M1, M3, M4, M7) (#78) |
| `7130659` | `e0fb200` | fix(security): Epic B — auth/session (H2, H3, H8, M5, M6) (#79) |
| `6cb0c62` | `ee2a7f8` | build(deps): bump lucide-react from 1.22.0 to 1.23.0 in /frontend (#84) |
| `22147cc` | `d5cd15a` | build(deps-dev): bump @tailwindcss/postcss in /frontend (#80) |
| `0ee40f5` | `8dea9e6` | build(deps-dev): bump vite from 8.1.0 to 8.1.3 in /frontend (#81) |
| `86d1014` | `07e45d2` | build(deps-dev): bump @types/node from 26.0.1 to 26.1.0 in /frontend (#82) |
| `12c4615` | `dd6ee54` | build(deps-dev): bump tailwindcss from 4.3.1 to 4.3.2 in /frontend (#83) |
| `9fa03b2` | `357df89` | fix(audit): contrast, mobile sidebar, focus, version — audit score 11→14 |
| `8628f6c` | `c6bdac7` | fix(audit): global contrast override for slate-400, last missing focus ring |
| `fe2e122` | `cf42341` | feat(ui): extract Button, Input/Field/Select, Badge, Card components; migrate employees + apps pages |
| `cd24e85` | `02d8240` | feat(ui): migrate employees and apps pages to use extracted UI components |
| `32b7142` | `7bf198c` | fix(layout): consistent card padding, app button row, audit filter spacing + responsive table |
| `bab4f2b` | `f3d5235` | feat(colorize): semantic CSS tokens, CSS-var Badge/Button, prefers-color-scheme |
| `af073f7` | `09e3757` | fix: bump deprovision button touch target to 44px |
| `0d2295c` | `88e9db7` | docs: add PRODUCT.md, DESIGN.md, live config, design sidecar |
| `1894517` | `3fbc5b6` | feat(polish): semantic Tailwind aliases, fix detector findings, bump touch targets |
| `1d0335a` | `2e0b9d9` | fix: OnboardForm null→undefined type error in CI |
| `ab312f8` | `2a650c8` | fix: add matchMedia stub for jsdom test environment |
| `8757833` | `e089e3a` | ci: bump Go to 1.26 for TLS CVE patches |
| `c4996da` | `34db450` | ci: make govulncheck and npm audit advisory-only (continue-on-error) |
| `160368f` | `5b4fbf8` | fix(security): close review findings for AuthZ, BFF, HA, and posture |
| `23fdd86` | `5f28b5d` | fix(security): HMAC-sign freecloud-device-id cookies |
| `c20ac96` | `7b47962` | fix(ci): stop leader electors from starving the DB pool |
| `77b908d` | `0f2a7db` | test(leader): real-pg unlock failover integration; hard-fail govulncheck |
| `fcb4de0` | `b67a20c` | ci(e2e): run single-replica smoke on PR; mint device-cookie e2e |
| `d4a11b6` | `214df3a` | feat(security,obs): optional DEVICE_COOKIE_SECRET; leader HA dashboard |
| `9388cc0` | `6d17583` | feat(mdm): org-scoped Fleet teams (fleet_team_orgs) + production loop batch |
| `68040cd` | `c9fe6de` | test(mdm): harden fleet team org gates; document backfill and cookie env |
| `0ec19e2` | `2f02754` | loop(G09): BFF double-encoded traversal reject; requireMFA same-origin test |
| `150535b` | `6f66746` | loop(G07): unit-test SIEM leader gate skips poll when not leader |
| `1a4daa3` | `2598b4e` | loop(G06): unit-test fireReviewSchedule campaign creation path |
| `6388806` | `34ff6e4` | loop(G01-G02,G08,G10): fleet team name/desc validate; hostId trim; ListTeams org fail-closed |
| `a956f91` | `86dbc22` | loop(G01-G05,G08,G10): production-path tests for fleet team org gates |
| `fafc273` | `5d4781e` | loop(G01-G10): baton Done entries for ten production-path cycles |
| `2fd9beb` | `3dd5fea` | docs(baton): restore append-only Done history for sequential H-cycles |
| `5d62d22` | `b360329` | loop(H01): ValidatePolicyID for AssignTeamPolicy |
| `e6d173e` | `555b7df` | loop(H02): ValidateHostID for MoveHostToTeam |
| `38f647f` | `1eaf5ac` | loop(H03): ValidateTeamDescription for CreateTeam |
| `1ef4839` | `f0ab67b` | loop(H04): stable-sort ListTeams by fleet team ID |
| `b87f931` | `ba0d413` | loop(H05): test fireReviewSchedule no-op on lost leader race |
| `d317bd5` | `0a45d91` | loop(H06): SIEM leader-gate tests and debug skip log |
| `787de70` | `295cce5` | loop(H07): BFF test rejects empty path segments |
| `6b8e8f1` | `7b69631` | loop(H08): getMFAStatus same-origin privileged API test |
| `0bac4d9` | `f5cc45b` | loop(H09): test system-admin bypasses fleet_team_orgs ownership query |
| `4626c46` | `de16d77` | loop(H10): full go test + frontend verify; baton H01-H10 Done |
| `e7a49de` | `9973593` | loop(H10): ParsePositiveTeamID for team path params (overflow-safe) |
| `1363fa3` | `a23e75d` | loop(I01): ValidateHostID rejects control characters on MoveHost path |
| `828d099` | `cf311bc` | loop(I02): ValidatePolicyID rejects control characters on AssignTeamPolicy |
| `cf15cf8` | `0794b29` | loop(I03): enforce maxHostIDsPerMove constant on MoveHostToTeam |
| `a4aaa52` | `973bfbf` | loop(I04): production-path test ListPolicies empty for non-system-admin |
| `30d061c` | `e1de1a8` | loop(I05): RemoteLock validates device id via ValidateHostID |
| `93f9ec7` | `743d9bf` | loop(I06): BFF test rejects encoded slash path segments |
| `1caad9a` | `c32db3d` | loop(I07): RemoteRestart validates device id via ValidateHostID |
| `03e7f59` | `5d434ce` | loop(I08): fleet_team_orgs bulk-import CSV parse helper (ops) |
| `aea5bb7` | `69edf82` | loop(I09): RemoteClearPasscode validates device id via ValidateHostID |
| `dc1497c` | `4ffe80c` | loop(I10): RemoteLockWithMessage ValidateHostID; MoveHost team id path test |
| `123e3f6` | `3189817` | loop(J01): import-fleet-teams CLI for fleet_team_orgs CSV backfill |
| `c611996` | `bfc9fc9` | loop(J02): GetDeviceCommandHistory ValidateHostID and history limit const |
| `b35bbcd` | `a281870` | loop(J03): ValidateUserID for GetDeviceSoftware user path id |
| `a679d0c` | `1e09104` | loop(J04): ValidateUserID for GetUserCompliance user path id |
| `6451f18` | `6faec04` | loop(J05): BFF test rejects encoded backslash path segments |
| `0f85dc0` | `18054ca` | loop(J06): SIEM pollBatchSize constant for audit poll LIMIT |
| `b7d94fa` | `67c4cd9` | loop(J07): review schedule runner skips non-leader ticks with debug log |
| `ccbef9c` | `6758e88` | loop(J08): cap fleet_team_orgs CSV import at MaxMappingCSVRows |
| `55cc9de` | `78cdc24` | loop(J09): maxDueReviewSchedules constant for due schedule query LIMIT |
| `04cbfd5` | `a9b4b96` | loop(J10): maxTeamDisplayNameLen CreateTeam; command-history BFF path test |
| `940ec21` | `b2670c9` | fix(ci): remove unused body assignment in I10 lock-message test |
| `9e8d437` | `23ec43b` | loop(K01): ValidateUserID on GetMFAStatus |
| `0be76dc` | `6a4a7b6` | loop(K02): ValidateUserID on RequireMFA |
| `c4ec3b7` | `9672aa5` | loop(K03): ValidateUserID on Offboard |
| `29f2b3b` | `1f43280` | loop(K04): ValidateUserID on GetUser |
| `1b1358b` | `1dcb1ea` | loop(K05): BFF reject overlong path segments |
| `4c1b61e` | `d385a08` | loop(K06): ValidateHostID on enrollment callback host_id |
| `946c6df` | `8fe8add` | loop(K07): ValidateUserID on EvaluateAccess |
| `3746a51` | `b7ce464` | loop(K08): reject duplicate fleet_team_id in mapping CSV |
| `c3e972b` | `a4339ed` | loop(K09): ValidateUserID on PatchUser |
| `3bc1818` | `bf08f7b` | loop(K10): ResetPassword path test; MFA/isolation tests use real UUIDs |
| `f7c4184` | `2e11bec` | loop(L01): ValidateUserID on AssignUserToGroup |
| `28b7017` | `0ee6d05` | loop(L02): ValidateUserID and ValidateOpaqueID on UnassignUserFromGroup |
| `860846f` | `b3ab202` | loop(L03): ValidateUserID on AssignRealmRoleToUser |
| `f4dca21` | `8862f08` | loop(L04): ValidateUserID and ValidateOpaqueID on AssignApp |
| `db4e9c0` | `5fe36f7` | loop(L05): ValidateHostID on EvaluateAccess deviceId |
| `31db78c` | `cb92edf` | loop(L06): BFF max path segment count |
| `0b210d9` | `4addb7b` | loop(L07): cap enrollment_token length on Fleet callback |
| `90fa6c1` | `fd978d8` | loop(L08): ValidateOpaqueID on AssignRealmRole fields |
| `789f5ae` | `de06ce8` | loop(L09): ValidateUserID on AddOrgMember |
| `a19739e` | `ac9dd18` | loop(L10): ValidateOpaqueID on AssignUserToGroup groupId |
| `21ff4dd` | `82b10e9` | loop(M01): ValidateUserID on RemoveOrgMember |
| `6e4fd84` | `a07503f` | loop(M02): ValidateUserID on approval offboard payload |
| `7d287a9` | `2710a57` | loop(M03): ValidateOpaqueID on app access policy paths |
| `b06dcef` | `058b0a4` | loop(M04): ValidateUserID on review schedule path ids |
| `70d23eb` | `9311773` | loop(M05): ValidateUserID on RevokeAPIToken |
| `db2a819` | `7fd0924` | loop(M06): ValidateUserID on GetProvisioningConfig appId |
| `d7688b7` | `cf226fa` | loop(M07): BFF reject control chars in path segments |
| `f9943da` | `0c5124f` | loop(M08): ValidateOpaqueID on PreviewAppPolicy |
| `4bb2be1` | `521974b` | loop(M09): ValidateUserID on PortalRequestAccess appId |
| `75d1889` | `4b423cc` | loop(M10): ValidateOpaqueID on SAML IdP URL; schedule delete path test |
| `8fb53f4` | `c6976ff` | loop(N01): ValidateUserID on ListCampaignItems |
| `c54c6dd` | `7a41ae8` | loop(N02): DecideCampaignItem path-id validation tests |
| `7c3fd00` | `c06d30b` | loop(N03): ValidateUserID on DecideApproval |
| `1f76cc3` | `58d1770` | loop(N04): ValidateUserID on ListOrgMembers |
| `bd3a0d7` | `e82f6f5` | loop(N05): ValidateUserID on UpsertProvisioningConfig |
| `94eefc4` | `7aca4a8` | loop(N06): ValidateUserID on AdminDecideAccessRequest |
| `2a1e484` | `17bdd59` | loop(N07): BFF reject whitespace-padded path segments |
| `f6f3b4a` | `1e46eba` | loop(N08): ValidateUserID on ExportCampaign |
| `60dc4c6` | `fb33627` | loop(N09): ValidateOpaqueID on GetSAMLMetadata |
| `36cbb69` | `27c4a7e` | loop(N10): ValidateOpaqueID on MFA factor remove; CompleteCampaign test |
| `039bf90` | `1e07a23` | loop(O01): ValidateUserID on AddOrgMember orgId |
| `34d60fb` | `4c10a48` | loop(O02): ValidateUserID on ListProvisioningState |
| `db39a49` | `6a2e50a` | loop(O03): ValidateUserID on ResyncUser path ids |
| `43b81bb` | `144caea` | loop(O04): DryRunProvisioning userId validation path test |
| `159eff1` | `60f3e78` | loop(O05): ReconcileAllHandler appId validation path test |
| `d40b5c0` | `64232b0` | loop(O06): ValidateUserID on SCIM org path orgID |
| `01c16ee` | `432c1e5` | loop(O07): ValidateUserID on SCIMGetUser |
| `c826910` | `77744e5` | loop(O08): ValidateHostID on MintDeviceCookieValue |
| `1fe63e5` | `0937e73` | loop(O09): BFF reject triple-dot path segments |
| `7b62f4d` | `ca03435` | loop(O10): ValidateOpaqueID on SCIM group path ids |
| `68042da` | `1600bc8` | test: align SCIM/dry-run fixtures with ValidateUserID gates |
| `d3337dc` | `8c65add` | test(scim): use UUID user ids in SCIM conformance suite |
| `d19dd21` | `dd031b6` | feat(e2e,ops): fleet team mapping e2e, persistent mock teams, pilot go-live doc |
| `72f4e70` | `3569dd4` | build(deps-dev): bump vite from 8.1.3 to 8.1.5 in /frontend (#98) |
| `2dbd054` | `6b1c5c9` | build(deps): bump actions/setup-node from 6 to 7 (#92) |
| `96c4e94` | `9d5d91d` | build(deps): bump actions/setup-go from 6 to 7 (#93) |
| `58c25de` | `eebfadc` | build(deps-dev): bump @types/node from 26.1.0 to 26.1.1 in /frontend (#94) |
| `5f2c715` | `9d7ed33` | build(deps-dev): bump postcss from 8.5.16 to 8.5.20 in /frontend (#95) |
| `688a907` | `62ca2a9` | build(deps-dev): bump @tailwindcss/postcss in /frontend (#97) |
| `2bb9189` | `b539791` | build(deps): bump lucide-react from 1.23.0 to 1.27.0 in /frontend |
| `b4cb094` | `12acb54` | build(deps-dev): bump @vitejs/plugin-react in /frontend |
| `62d2228` | `f1c57e4` | build(deps-dev): bump postcss from 8.5.20 to 8.5.23 in /frontend |
| `ab3280c` | `5c108c6` | build(deps-dev): bump @testing-library/jest-dom in /frontend |
| `381273e` | `af41ff1` | build(deps): bump react from 19.2.7 to 19.2.8 in /frontend |
| `3a2e22a` | `114699c` | fix(sec): bump golang.org/x/text to v0.39.0 for govulncheck CVE |
| `58e0d13` | `a5be543` | Merge pull request #103 from FisiFla/dependabot/npm_and_yarn/frontend/react-19.2.8 |
| `d9f8228` | `f8a7582` | Merge pull request #102 from FisiFla/dependabot/npm_and_yarn/frontend/testing-library/jest-dom-7.0.0 |
| `1cb0fae` | `6c33e0e` | Merge pull request #101 from FisiFla/dependabot/npm_and_yarn/frontend/postcss-8.5.23 |
| `f9eee82` | `9782151` | Merge pull request #100 from FisiFla/dependabot/npm_and_yarn/frontend/vitejs/plugin-react-6.0.4 |
| `1ddbd6c` | `6369372` | Merge pull request #99 from FisiFla/dependabot/npm_and_yarn/frontend/lucide-react-1.27.0 |
| `829d092` | `b779b38` | fix: sync react-dom to 19.2.8 to match react (version mismatch breaks tests) |
| `74db24e` | `09a695d` | feat(ops): import-fleet-teams -apply live DB mode with atomic tx + tests |
| `844b06a` | `e2f86b4` | fix(handlers): validate UpdateReviewSchedule name, reorder body check before DB |
| `68b74ea` | `0bfc970` | feat(fleet): bound host software list at MaxSoftwarePerHost; complete body-ID audit |
| `030104e` | `52758e9` | feat(observability): review schedule runner metrics (ticks/fired) + tests |
| `e55014d` | `a123dc3` | docs(backlog): cycle4 — runner metrics done, GetHosts deferred |
| `b0ec4a0` | `f010a3c` | fix(bff): bound backend response buffering (50MiB default, env-tunable) + tests |
| `515cc41` | `9398113` | docs(backlog): cycle5 — BFF size guard done |
| `251b29b` | `6dc6302` | docs(backlog): cycle6 — whole-gate verification green (local + CI) |
| `efaa695` | `16ad014` | refactor(handlers): extract httputil package (respondJSON/respondError/validation) per HANDLERS_OWNERSHIP |
| `0abb38b` | `686c711` | docs(backlog): cycle7 — httputil extraction done, orgscope next |
| `f55c48d` | `a9ff7ae` | refactor(handlers): extract internal/orgscope (org-scoping gate core) per HANDLERS_OWNERSHIP step 2 |
| `21ba466` | `acb9709` | docs(backlog): cycle8 — orgscope extraction done, HANDLERS_OWNERSHIP complete |
| `b129ab8` | `4c186a7` | feat(observability): Grafana alerts for review-schedule runner (stalled + skipping) |
| `61407f7` | `809bd19` | docs(backlog): cycle9 — runner Grafana alerts done |
| `3f0ce87` | `0efa951` | feat(rbac): helpdesk gets PermManagePolicies (read+mutate within org) per product decision |
| `520112a` | `74c2406` | docs(backlog): cycle10 — helpdesk RBAC decision shipped |
| `f705e67` | `f81bbe5` | feat(security): nonce-based CSP — drop unsafe-inline/eval from script-src in prod |
| `0d145c4` | `3ed82a9` | docs(backlog): cycle11 — nonce CSP done |
| `af95005` | `1e60d27` | chore(deps): pin next-auth to exact 5.0.0-beta.31 for reproducible builds (no stable v5 yet) |
| `483660b` | `d58f432` | docs(backlog): cycle12 — next-auth pin done |
| `ad3a157` | `8ecf5bb` | test(rbac): helpdesk foreign-org device lock returns 404 — regression for cycle-10 grant |
| `3243a84` | `b9dd104` | docs(backlog): cycle13 — helpdesk device-actions audit + regression |
| `c4a11c3` | `29b0d29` | fix(fleet): bound doRequest response buffer at 10MiB — load-path hardening + test |
| `ade7adc` | `6b35a52` | docs(backlog): cycle14 — fleet response cap done |
| `832b5e2` | `5f22ff6` | fix(keycloak): bound response reads at 10MiB (SAML metadata, LDAP test, federation sync) + tests |
| `b7826bf` | `487daca` | docs(backlog): cycle15 — keycloak response caps done |
| `59f0b8c` | `673d705` | fix(load): shared httpx.ReadAllBounded — cap provisioning SCIM/GitHub, bootstrap, import CLI reads |
| `7134bdf` | `a706639` | docs(backlog): cycle16 — unbounded-read sweep complete |
| `63f0b6a` | `2011f11` | fix(audit): write org_id, canonicalise chain hash, fix time-window eval |
| `08d1f21` | `e845ff8` | fix: close out the full-review backlog (issues #117-#229) |
| `e1a2d5b` | `484ceaf` | docs(backlog): cycle18 — full-review closeout (issues #117-#229) |
| `637047d` | `41a329a` | fix(ci): restore green CI — staticcheck findings and Dockerfile FROM parse errors |
| `a0c17c0` | `fe583f6` | build(deps-dev): bump org.apache.maven.plugins:maven-shade-plugin (#232) |
| `accdd75` | `1d6a600` | build(deps-dev): bump org.glassfish.jersey.core:jersey-common (#239) |
| `0a15b36` | `7c78af6` | build(deps-dev): bump org.mockito:mockito-core (#241) |
| `c763337` | `27e28ea` | chore(deps): bump Go dependencies (supersedes #231, #234, #236, #237, #240) |
| `16c8f15` | `247964f` | chore(deps): bump frontend dev dependencies and setup-java |
| `6a88eb7` | `28166d9` | fix(ci): pin the Go toolchain to the patched go1.26.10 |
| `e77d02a` | `8507d5c` | fix(ci): own the Keycloak entrypoint as uid 1000 so the image builds |
| `16c2828` | `ddc6d23` | fix(e2e): mint the device cookie with the domain-separated key |
| `c24976b` | `bf78d0b` | fix(users): alias the paged users CTE columns — GET /api/v1/users returned 500 |
| `bd1fdeb` | `7fd064f` | docs(backlog): cycle19 — PR/branch cleanup and green CI |
| `8e8e81e` | `66a37f5` | chore: add .mailmap so all work is credited to the FisiFla account |
