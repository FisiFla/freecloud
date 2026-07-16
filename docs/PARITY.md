# FreeCloud vs JumpCloud — Feature Parity Matrix

FreeCloud is an open-source alternative to JumpCloud for teams that want full
control over their identity and device management stack. It covers the core
JumpCloud surface area — SSO, SCIM, MDM, audit, access governance, multi-org
tenancy, and multi-instance HA — self-hostable via Docker Compose. The
remaining deferrals (real VPS production deploy, live outbound-connector
tenant verification, hard realm-per-org isolation) are in the gaps section
below.

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
| Conditional access — max OS age days | ⚠️ Field rejected at write time until Fleet exposes OS age posture |
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
| Slack connector | ✅ Contract-verified against recorded SCIM fixtures; live-tenant run pending sandbox credentials |
| GitHub Org connector | ✅ Contract-verified against recorded API fixtures; live-tenant run pending sandbox credentials |
| Live connector verification tool (`make verify-provisioning-live`) | ✅ |

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
| API tokens with RBAC (org-scoped) | ✅ |
| RBAC roles (super-admin / helpdesk / auditor / read-only) | ✅ |
| Multi-organisation tenancy (shared realm + org isolation) | ✅ [ADR 0005](adr/0005-multi-tenant-shared-realm.md) |
| Org management UI + org switcher | ✅ |
| Per-org SCIM bearer tokens + org-scoped SCIM endpoints | ✅ |
| Multi-instance / HA (Redis rate limiting, decoupled migrations, leader election) | ✅ [ADR 0004](adr/0004-multi-instance-ha.md), proven by a two-replica e2e suite |
| Dark mode UI | ✅ |

### Operations

| Feature | Status |
|---|---|
| Docker Compose deploy | ✅ |
| Turnkey single-command deploy (auto-secrets + self-bootstrap) | ✅ |
| Decoupled schema migrations (`server migrate` + one-shot compose job) | ✅ |
| Live GeoIP (operator-supplied MaxMind GeoLite2, fail-closed) | ✅ |
| In-app Fleet / SMTP / IdP configuration | ✅ |
| First-run setup wizard (create first admin without touching env) | ✅ |
| Caddy TLS termination | ✅ |
| Observability (Prometheus + Grafana) | ✅ |
| Backup / restore | ✅ |

## Deferred / Known Gaps

| Gap | Notes |
|---|---|
| **Real VPS production deploy** | The stack is code-complete and turnkey (single `docker compose up`, auto-secrets, self-bootstrap wizard) — and, as of v1.7, HA-capable — but has **never been deployed to a real VPS** (deferred for the seventh consecutive release). All validation, including the two-replica HA suite, runs in local/CI Docker Compose. This is the largest unquantified risk in the project. |
| **Realm-per-org isolation** | v1.7 ships multi-org tenancy in a **shared Keycloak realm**: org isolation is enforced in FreeCloud's data model, RBAC, API, SCIM, and UI, proven by a cross-org isolation test suite. Hard isolation (separate realm per org: separate user namespaces, login flows, token issuers) is designed-for but deferred — see [ADR 0005](adr/0005-multi-tenant-shared-realm.md). Known limitation: user emails are unique across the whole deployment, not per org. |
| **Slack/GitHub live-tenant verification** | Connectors are contract-verified against recorded API fixtures (create/update/deactivate/offboard) and an optional live-verification tool exists (`make verify-provisioning-live`, gated on sandbox credentials). A live run still hasn't happened: GitHub needs a sandbox org token; Slack SCIM additionally requires a paid (Business+) plan. |

Gaps retired in v1.7: HA / multi-instance ([ADR 0004](adr/0004-multi-instance-ha.md)),
authenticated e2e round-trips (seeded admin-JWT path, e2e-only), SPI client-IP
forwarding (trusted-proxy resolution proven end-to-end incl. spoof rejection),
and live GeoIP (operator-supplied GeoLite2 via `GEOIP_MMDB_PATH`, fail-closed).

## Versioning

This matrix reflects **FreeCloud v1.7.0 (2026-07-02)**.
