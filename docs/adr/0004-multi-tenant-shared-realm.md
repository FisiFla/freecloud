# ADR 0004: Multi-tenant foundations — shared Keycloak realm, org isolation in FreeCloud's data model

## Status

Accepted (2026-07, v1.7 Epic C).

## Context

Through v1.6, FreeCloud is single-tenant: one Keycloak realm, one flat set of
users/devices/apps/policies, one implicit "organization." v1.7 Epic C adds
MSP-style multi-tenancy — several customer organizations served from one
FreeCloud deployment — without requiring an operator to stand up a new
Keycloak realm (or a new deployment) per customer.

Two architectural options exist for isolating tenants:

1. **Realm-per-org**: each organization gets its own Keycloak realm. Keycloak
   natively isolates users, credentials, and sessions per realm.
2. **Shared realm + org_id**: one Keycloak realm for all organizations; org
   isolation is enforced entirely in FreeCloud's own Postgres schema and API
   layer, with Keycloak staying org-unaware.

## Decision

**Shared realm now. Design for realm-per-org later, behind the same org
abstraction. Do not build realm-per-org in this epic.**

Concretely:

- One Keycloak realm continues to serve every organization in a FreeCloud
  deployment. Keycloak users are unique by `email` **realm-wide**, not
  per-org — see "Accepted limitation" below.
- `organizations` and `org_memberships` are new Postgres tables (Migration043,
  `backend/internal/db/schema.go`). Every tenant-scoped table gains a NOT NULL
  `org_id` foreign key. See the migration's own doc comment for the full
  tenant-scoped-table inventory and the zero-downtime backfill sequence.
- The org context for a request is resolved by
  `middleware.OrgContextMiddleware` from `X-Org-Id` (validated against the
  caller's memberships) or the caller's sole membership, and is fail-closed:
  no resolvable org context is a 403, never an implicit fall-through to
  global/cross-org data.
- The Keycloak authenticator SPI (`keycloak-authenticator/`) remains entirely
  org-unaware. `/api/v1/access/evaluate` resolves the org from the evaluated
  *user*, not from the SPI caller — the SPI just forwards a user ID.

### Why shared-realm-now

- **Zero new infrastructure per tenant.** Realm-per-org means every new
  customer requires a Keycloak realm-provisioning step (client registration,
  IdP config, SMTP, themes, ...) — meaningful operational weight for an
  MSP onboarding customers self-service. Shared-realm-now means "add a
  customer" is one `organizations` row plus membership rows.
- **Reuses 100% of the existing single-tenant code paths.** Every handler,
  the SPI, SCIM, and the reconcile/notify/snapshot background jobs already
  assume one realm. Isolating at the Postgres/API layer is an additive `org_id`
  column and a `WHERE org_id = $ctx` filter — not a rearchitecture of the
  Keycloak integration.
- **Matches the stated non-goal.** The epic brief is explicit: realm-per-org
  is out of scope for this round; document the path, don't build it.

### Accepted limitation: realm-wide unique email

Keycloak enforces `email` uniqueness per realm. Under shared-realm-now, this
becomes a **cross-organization** constraint: two different customer orgs in
the same FreeCloud deployment cannot each onboard a user with the same email
address. This is the direct, accepted cost of not doing realm-per-org.

Mitigations available without changing the realm model, if this becomes a
real-world blocker:

- Keycloak supports `loginWithEmailAllowed=false` + a synthetic per-org
  username scheme (e.g. `org-slug+localpart`), decoupling the login
  identifier from the "real" email stored as a user attribute. Not built in
  this epic.
- Realm-per-org (below) removes the constraint entirely, at the cost
  described.

## What realm-per-org would require (future work, not built here)

If the shared-realm model's limitations (email uniqueness, blast radius of a
realm-wide Keycloak outage, per-org branding/IdP needs, per-org session
policy) become blocking, migrating to realm-per-org requires at minimum:

| Constraint | Required change |
|---|---|
| One `keycloak.Client` per backend process, pointed at one realm | Parameterize the Keycloak client by org — either a per-request client built from `OrgContext.OrgID -> realm name`, or a small pool of clients keyed by realm. `keycloak.KeycloakClientInterface` would need an org/realm parameter threaded through every call. |
| `AuthMiddleware` validates JWTs against one fixed issuer (`{keycloakURL}/realms/{realm}`) | The issuer becomes dynamic per request. A JWT's `iss` claim would need to be resolved to an org (e.g. via a realm-to-org lookup table) *before* signature validation, then that realm's JWKS used to verify — inverting today's flow where org resolution happens after auth. |
| Bootstrap (`internal/bootstrap`) provisions one realm on startup | Bootstrap becomes a per-org, on-demand operation (triggered by `POST /api/v1/orgs`), not a startup-time action. Realm creation, client registration, and the service-account setup all move into the org-creation code path. |
| SCIM / access-eval bearer tokens are realm-agnostic service tokens | No change needed — these already flow through FreeCloud's own token tables, which are already org-scoped (C4). |
| `org_id` foreign keys throughout the schema | No change needed — the Postgres-side isolation model (this ADR's core contribution) is exactly the abstraction realm-per-org would sit behind. This is *why* build the org abstraction now: migrating the Keycloak layer later doesn't require re-touching every handler's `WHERE org_id = $ctx` filter. |

The `OrgContext` abstraction (`middleware.GetOrgContext`) is deliberately the
seam: everything downstream of it (handlers, SCIM, audit, analytics) reads
`OrgContext.OrgID` and does not know or care whether that org lives in a
shared realm or its own. Moving to realm-per-org later is a change to how
`OrgContext` gets resolved and how the Keycloak client is selected — not a
change to any tenant-scoped query.

## Consequences

- Every tenant-scoped Postgres query must filter on `org_id`. A missed filter
  is a cross-org data leak, not a crash — this is the failure mode the C2
  route-coverage-guard extension and the C5 isolation test suite exist to
  catch mechanically rather than relying on review alone.
- The audit log keeps **one global hash chain** across all organizations
  (not one chain per org) — see the audit_logs section of Migration043's doc
  comment. `org_id` is a filter column on reads; chain integrity
  verification (`VerifyAuditChain`) walks the full unfiltered chain. This is
  a deliberate simplicity choice: per-org chains would need per-org anchor
  bookkeeping (`audit_chain_anchors` is currently a singleton) for no
  isolation benefit, since the chain's own tamper-evidence property does not
  depend on partitioning by tenant.
- A single Keycloak outage affects every organization in the deployment
  (shared fate). Accepted for this epic; realm-per-org would reduce blast
  radius at the infrastructure cost described above.
- New organizations are created purely in Postgres (`POST /api/v1/orgs`) —
  no Keycloak realm-provisioning latency or failure mode on the "add a
  tenant" path.
