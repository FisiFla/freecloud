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
  `CORS_ORIGIN`. The frontend refuses to start in production with the placeholder
  `AUTH_SECRET` or a non-`https` API URL.
- **API authentication.** Every `/api/v1/*` endpoint (except liveness/readiness
  and the HMAC-authenticated Fleet callback) requires a Keycloak-issued JWT,
  validated against the realm JWKS with signature, `exp`, issuer, and audience
  checks. Management endpoints additionally require the `admin` realm role.
- **Fleet enrollment webhook.** `POST /api/v1/fleet/enrollment-callback` is
  authenticated by an HMAC-SHA256 signature over the request body keyed by
  `FLEET_WEBHOOK_SECRET` (constant-time compared); an unset secret rejects all
  callbacks.
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
