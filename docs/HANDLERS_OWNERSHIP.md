# Handlers package ownership (split plan)

`backend/internal/handlers` is a large co-located package (~HTTP routes + domain
orchestration + SQL). Full package splits need import-cycle care; until then,
treat these **ownership domains** as the unit of change and review:

| Domain | Primary files | Notes |
|---|---|---|
| **Lifecycle** | `onboarding.go`, `bulk_onboarding.go`, `offboarding.go`, `enrollment.go` | Saga + Fleet callback; highest blast radius |
| **Access / posture** | `access_eval.go`, `access_policy*.go`, `device_identity.go`, `device_check.go` | SPI-facing; signed cookies |
| **SCIM** | `internal/scim/` (impl); `scim*.go` in handlers are delegating shims | Inbound Users/Groups; org-scoped tokens; onboarding write via `scim.Host` seam |
| **Devices / MDM** | `device_actions.go`, `device_policy.go` | Fleet lock/wipe/teams; `fleet_team_orgs` |
| **Apps / provisioning** | `apps.go`, `app_catalog.go`, `provisioning*.go` | Outbound connectors + SSRF allowlist |
| **Governance** | `access_review.go`, `review_schedules.go`, `approvals.go`, `portal.go` | Campaigns + self-service |
| **Admin settings** | `account_policy.go`, `api_tokens.go`, `federation.go`, `smtp_config.go`, `fleet_config.go`, `identity_providers.go`, `orgs.go`, `setup.go` | Super-admin surfaces |
| **Shared** | `handler.go`, `routes.go`, `org_scope.go`, `helpers.go`, `audit_*.go` | Wiring + isolation helpers |

## Split order (when ready)

1. ✅ Extract `internal/httputil` (respondJSON, ValidationError) — zero domain deps. *(16ad014)*
2. ✅ Extract `internal/orgscope` or keep helpers in handlers until call sites stabilize. *(a9ff7ae)*
3. ✅ Move SCIM to `internal/scim` last among big domains (most self-contained HTTP surface).
   **3a:** filter grammar + discovery endpoints moved to `internal/scim`
   (`filter.go`, `discovery.go`, shared URNs/`Meta`/`Respond` in `scim.go`);
   `handlers/scim_filter.go` + `handlers/scim_discovery.go` are delegating shims.
   **3b:** Users/Groups endpoints + Bearer [REDACTED] moved to `internal/scim`
   (`users.go`, `groups.go`, `middleware.go`, `types.go`, `service.go`).
   The `persistOnboard` blocker is resolved by the `scim.Host` seam: the
   onboarding saga write, audit attribution, and shared validators stay in
   handlers and are injected via the `scimHost` adapter in
   `handlers/scim.go`, which also holds the type aliases + delegating
   `Handler` methods so routes and tests keep their original names. The
   saga itself did not move; if onboarding ever extracts, only the
   adapter changes, not `internal/scim`.
4. Keep `routes.go` in a thin `internal/api` or handlers root that imports domains.

Do **not** split for its own sake while a domain still churns weekly.
