# Security Policy

FreeCloud manages identities and can disable accounts and wipe devices, so its
own security posture is safety-critical. This document describes how to report
issues and the security model the project is built around.

## Reporting a Vulnerability

Please report security issues privately via GitHub's **"Report a vulnerability"**
(Security → Advisories) on the repository, rather than opening a public issue.
Include reproduction steps and the affected version/commit. We aim to acknowledge
reports within a few days.

## Security Model

- **Fail-closed configuration.** The backend treats any `APP_ENV` other than
  `development`/`test` (including unset) as production and refuses to start on
  insecure defaults: the default database DSN, a `DATABASE_URL` without an
  explicit `sslmode=require|verify-ca|verify-full`, the `admin-cli`
  Keycloak client, a localhost Keycloak URL, or a missing
  `FLEET_API_TOKEN` / `FLEET_WEBHOOK_SECRET` / `SCIM_BEARER_TOKEN` /
  `ACCESS_EVAL_TOKEN` / `PROVISIONING_MASTER_KEY` / `CORS_ORIGIN` /
  `REDIS_URL` / `KEYCLOAK_AUDIENCE` or a weak `KC_BOOTSTRAP_PASSWORD`, and
  known placeholder/trivial secret values (`changeme`, `secret`, the
  documented e2e/dev defaults, single characters) are rejected outright.
  `KEYCLOAK_CLIENT_SECRET` is **optional**: the bootstrap engine provisions the
  confidential service client and manages its own secret at runtime. The
  frontend refuses to serve in production with the placeholder/short
  `AUTH_SECRET`, or without `INTERNAL_API_URL` set (no silent localhost
  fallback). There is no https-enforcement on the BFF-to-backend URL
  itself: the BFF proxies API calls to the backend as
  `http://backend:8080` over the internal compose network, attaching the
  caller's `Authorization` header server-side. Residual risk: that hop
  relies on container-network isolation — anything able to observe the
  internal network sees API tokens and payloads in cleartext.
- **Secrets are delivered as files.** The production stack mounts the
  auto-generated secrets (`POSTGRES_PASSWORD`, `KC_ADMIN_PASSWORD`,
  `AUTH_SECRET`, `SCIM_BEARER_TOKEN`, `ACCESS_EVAL_TOKEN`,
  `FLEET_WEBHOOK_SECRET`, `PROVISIONING_MASTER_KEY`, `REDIS_PASSWORD`)
  plus the operator-provided `FLEET_API_TOKEN` (hand-added to
  `.secrets/secrets.env`, mirrored one-per-file by the generator) as
  Docker secrets and reads them through the `<VAR>_FILE` convention, so
  the values are not visible in `docker inspect`. The database password
  travels the same way: `DATABASE_URL` carries no password and the
  backend/migrate inject it from `DATABASE_PASSWORD_FILE` at config
  load, and likewise `REDIS_URL` stays password-less while the backend
  injects `REDIS_PASSWORD_FILE` at config load. The backend Go process
  reads the files directly, so backend secrets are not in its process
  environment; the frontend and Keycloak entrypoints shell-`export`
  their secrets (`AUTH_SECRET`, `KC_BOOTSTRAP_ADMIN_PASSWORD`, ...),
  so those values ARE visible in `/proc/<pid>/environ` of the
  respective container processes. The backend also supports
  `<VAR>_VAULT_PATH` (HashiCorp Vault) — see
  [docs/SECRETS.md](docs/SECRETS.md).
- **API authentication.** Authenticated `/api/v1/*` endpoints require a
  Keycloak-issued JWT, validated against the realm JWKS with signature, `exp`,
  issuer, and audience checks. Sensitive routes additionally require explicit
  RBAC permissions. Public/service exceptions are intentionally narrow:
  liveness/readiness, the rate-limited forgot-password endpoint, the
  HMAC-authenticated Fleet callback, the dedicated-bearer access-evaluation
  endpoint, and the dedicated-bearer SCIM surface.
- **Fleet enrollment webhook.** `POST /api/v1/fleet/enrollment-callback` is
  authenticated by an HMAC-SHA256 signature over the request body keyed by
  `FLEET_WEBHOOK_SECRET` (constant-time compared); an unset secret rejects all
  callbacks.
- **Device identity cookie.** `freecloud-device-id` is an HMAC-signed v1 value
  (`DEVICE_COOKIE_SECRET` or fallback `FLEET_WEBHOOK_SECRET`); `POST /api/v1/access/evaluate` verifies the
  signature before trusting the Fleet host ID (forged bare host IDs are denied
  when the secret is configured).
- **Fleet teams multi-tenant.** Team create records `fleet_team_orgs`; non–system-admin
  list/mutate is limited to teams mapped to the caller's org (shared Fleet API remains global).
- **Keycloak service account.** `bootstrap.go` grants the
  `freecloud-service` service account the realm-management `realm-admin`
  composite role. Narrower grants were tried first but return 403 on
  operations the backend performs on the operator's behalf (realm-role
  reads, users-by-role queries, realm SMTP updates, identity-provider
  management). Consequence: a leaked service credential is full realm
  administration — guard `KEYCLOAK_CLIENT_SECRET` accordingly.
- **Auditability.** Privileged actions (onboard, offboard, app create/assign,
  device enroll) are written to an append-only `audit_logs` table; onboarding
  binds the audit write into the same transaction as the user row. The
  audit hash chain is keyed in production: each new row also stores an
  HMAC derived from `PROVISIONING_MASTER_KEY`, and verification requires
  it on the chain head and on every row after the first keyed one, so a
  party with database write access cannot rewrite or re-chain entries
  without the server-side key. Rows written before keying are covered
  by the plain hash chain only, and deleting rows from the tail of the
  log is not detected.
- **First-admin bootstrap.** The setup endpoint requires the generated
  `SETUP_TOKEN` in production and refuses once any admin exists (local
  database or Keycloak); errors while checking fail closed. With posture
  enforcement on, an admin-role user with no enrolled device may sign in
  only until the first device enrolls anywhere in the deployment (app
  policy conditions still apply, and each such sign-in is audited). The
  window does not reopen: a consumed enrollment token keeps it closed
  even if device mappings are removed later. Enroll the first admin's
  device promptly.
- **Encrypted, verified backups.** `scripts/backup-bundle.sh` refuses to
  write an unencrypted bundle unless `--allow-plaintext` is passed
  explicitly; with `BACKUP_PASSPHRASE` set the bundle is encrypted
  (encrypt-then-MAC) with HMAC sidecars, and
  `scripts/nightly-backup-verify.sh` proves a backup restores end to end.
- **Federated sign-out.** The dashboard signs out through
  `/api/auth/federated-logout`, which clears the local Auth.js session
  and navigates the browser to the Keycloak `end_session` endpoint,
  ending the Keycloak session — not just the dashboard cookie.
- **Hardened outbound HTTP.** Admin-configured HTTP destinations (e.g.
  the Fleet server URL) are fetched through `httpx.NewOutboundClient`,
  which re-validates every request URL at send time, refuses blocked
  IPs at dial time, and re-validates each redirect hop (forbidding
  https→http downgrades).
- **Super-admin-only policy writes.**
  `PUT /api/v1/apps/{appId}/policy` requires `PermManageAccountPolicy`
  (super-admin): a policy change can disable an app's device-posture
  gates, so it is a security-policy change, not routine app
  administration.
- **Org-confined API tokens.** Tokens are created bound to the caller's
  org (`oc.OrgID`, never a request parameter) and every read scopes by
  `org_id`.
- **Transport.** Production runs behind a TLS-terminating reverse proxy (Caddy),
  Postgres requires TLS (`sslmode=require`), and security response headers + a
  CSP are set on every response. Note that `sslmode=require` encrypts the
  connection **without verifying the server certificate** (the Postgres image
  generates a self-signed certificate on first boot) — see
  [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#postgresql-tls-sslmode) for the
  residual MITM risk on the internal network and the documented way to
  strengthen it to `verify-full` with a distributed CA.

## Supported Versions

This project is pre-1.0; security fixes are applied to `main`. Pin to a released
image tag and watch releases for advisories.
