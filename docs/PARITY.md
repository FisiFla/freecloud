# FreeCloud vs JumpCloud — Feature Parity Matrix

FreeCloud is an open-source alternative to JumpCloud for teams that want full
control over their identity and device management stack. It covers the core
JumpCloud surface area — SSO, SCIM, MDM, audit, and access governance — while
being self-hostable on a single Docker Compose host. Some JumpCloud capabilities
(HA/multi-instance, live outbound-sync connectors, real VPS production deploy)
are deferred; see the gaps section below.

## Feature Matrix

### User Lifecycle

| Feature | Status |
|---|---|
| Employee onboard / offboard | ✅ |
| Bulk CSV onboarding | ✅ |
| Approval workflow | ✅ |
| Self-service portal (access requests, password reset) | ✅ |

### Identity & SSO

| Feature | Status |
|---|---|
| OIDC SSO applications | ✅ |
| SAML SSO applications | ✅ |
| SAML IdP-initiated SSO URL | ✅ |
| SAML IdP metadata (XML download) | ✅ |
| App catalog templates | ✅ |
| Conditional access / device posture enforcement | ✅ |
| Conditional access — time / network / geo conditions | ✅ |
| Conditional access policy dry-run (preview) | ✅ |

### MFA

| Feature | Status |
|---|---|
| TOTP | ✅ |
| WebAuthn | ✅ |
| Admin-enforced MFA | ✅ |
| Self-service MFA management | ✅ |
| Recovery codes | ✅ |

### Directory Sync (Inbound)

| Feature | Status |
|---|---|
| SCIM 2.0 inbound provisioning | ✅ |
| SCIM Groups | ✅ |
| SCIM filter support | ✅ |
| SCIM discovery / ServiceProviderConfig | ✅ |
| LDAP / Active Directory federation (via Keycloak) | ✅ |

### Outbound Provisioning

| Feature | Status |
|---|---|
| Generic SCIM 2.0 connector | ✅ |
| Provisioning dry-run preview | ✅ |
| Reconcile-all (force re-sync every user) | ✅ |
| Slack connector | ⚠️ Config stored; live sync deferred |
| GitHub Org connector | ⚠️ Config stored; live sync deferred |

### Device Management (MDM)

| Feature | Status |
|---|---|
| Device enrollment via FleetDM | ✅ |
| Remote lock | ✅ |
| Remote wipe (on offboard) | ✅ |
| Restart | ✅ |
| Lock with message | ✅ |
| Clear passcode | ✅ |
| Software inventory | ✅ |
| Posture / compliance check | ✅ |
| Fleet teams | ✅ |
| Fleet policies | ✅ |

### Audit & Compliance

| Feature | Status |
|---|---|
| Audit log | ✅ |
| Audit log export (CSV / JSON) | ✅ |
| Audit log date-range filter | ✅ |
| Audit hash-chain integrity | ✅ |
| Compliance dashboard | ✅ |
| Compliance rate (real, from device posture cache) | ✅ |
| Analytics snapshots | ✅ |

### Access Governance

| Feature | Status |
|---|---|
| Per-app access policy | ✅ |
| Access review campaigns | ✅ |
| Campaign export (CSV / JSON) | ✅ |
| Recurring review schedules | ✅ |
| Approval workflow | ✅ |
| Access requests (self-service portal) | ✅ |

### Account Policies

| Feature | Status |
|---|---|
| Password policy | ✅ |
| Brute-force protection | ✅ |

### Administration

| Feature | Status |
|---|---|
| API tokens with RBAC | ✅ |
| RBAC roles (super-admin / helpdesk / auditor / read-only) | ✅ |
| Multi-instance / HA | ❌ Documented in [ADR 0003](adr/0003-single-instance.md) |
| Dark mode UI | ✅ |

### Operations

| Feature | Status |
|---|---|
| Docker Compose deploy | ✅ |
| Turnkey single-command deploy (auto-secrets + self-bootstrap) | ✅ |
| In-app Fleet / SMTP / IdP configuration | ✅ |
| First-run setup wizard (create first admin without touching env) | ✅ |
| Caddy TLS termination | ✅ |
| Observability (Prometheus + Grafana) | ✅ |
| Backup / restore | ✅ |

## Deferred / Known Gaps

| Gap | Notes |
|---|---|
| **Real VPS production deploy** | The stack is code-complete and turnkey (single `docker compose up`, auto-secrets, self-bootstrap wizard) but has never been deployed to a real VPS. All validation has been done locally via Docker Compose. |
| **Multi-tenant / organisation isolation** | v1.6 supports a single organisation. Multi-tenant isolation (separate Keycloak realms per org, tenant-scoped SCIM endpoints) is deferred; see the single-instance ADR for scope. |
| **Slack live-tenant sync** | The Slack outbound connector stores configuration and token, but does not yet perform live user provisioning or de-provisioning on group changes. |
| **GitHub Org live-tenant sync** | Same as Slack — connector stub only; no live sync loop implemented. |
| **HA / multi-instance** | The in-memory rate limiter and startup-migration pattern require a single backend instance. See [ADR 0003](adr/0003-single-instance.md) for the full rationale and what HA would require. |
| **Authenticated e2e round-trips** | The e2e harness has no admin-JWT login path (its bearers are opaque tokens scoped to the SCIM and access-eval endpoints). Admin-gated routes — including provisioning config and federation CRUD — are therefore e2e-covered at the smoke level (route wired + auth-gated against the live stack), consistent with the rest of the suite. A full authenticated provisioning round-trip and live LDAP sync are deferred pending an e2e admin-auth path. |
| **SPI client-IP forwarding** | The Keycloak authenticator SPI receives the client IP for network-condition evaluation, but only trusts `X-Forwarded-For` when `TRUST_PROXY=true`. A direct (non-proxied) SPI call will see the loopback IP and geo/network conditions will evaluate against 127.0.0.1. |
| **Live GeoIP resolution** | Geo conditions in conditional-access policies compare against a static IP→country lookup. A production-grade MaxMind GeoLite2 integration is deferred; the current lookup covers common test IPs only. |

## Versioning

This matrix reflects **FreeCloud v1.6.0 (2026-06-23)**.
