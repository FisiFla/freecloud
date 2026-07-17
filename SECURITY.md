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
  insecure defaults: the default database DSN, `sslmode=disable`, the `admin-cli`
  Keycloak client, a localhost Keycloak URL, or a missing
  `KEYCLOAK_CLIENT_SECRET` / `FLEET_API_TOKEN` / `FLEET_WEBHOOK_SECRET` /
  `SCIM_BEARER_TOKEN` / `ACCESS_EVAL_TOKEN` / `CORS_ORIGIN`. The frontend refuses
  to start in production with the placeholder `AUTH_SECRET` or a non-`https` API
  URL.
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
- **Least privilege.** The Keycloak service account is granted only
  `manage-users` + `manage-clients`, not the `realm-admin` super-role.
- **Auditability.** Privileged actions (onboard, offboard, app create/assign,
  device enroll) are written to an append-only `audit_logs` table; onboarding
  binds the audit write into the same transaction as the user row.
- **Transport.** Production runs behind a TLS-terminating reverse proxy (Caddy),
  Postgres requires TLS (`sslmode=require`), and security response headers + a
  CSP are set on every response.

## Supported Versions

This project is pre-1.0; security fixes are applied to `main`. Pin to a released
image tag and watch releases for advisories.
