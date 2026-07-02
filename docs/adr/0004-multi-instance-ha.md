# ADR 0004: Multi-instance HA — supersedes ADR 0003

## Status

Accepted (2026-07). **Supersedes [ADR 0003](0003-single-instance.md)**, which
is now marked Superseded below.

## Context

ADR 0003 capped FreeCloud v1 at exactly one backend replica, for two
concrete reasons:

1. The rate limiter (`internal/middleware/ratelimit.go`) kept per-replica
   in-memory counters, so N replicas gave a client an effective budget of
   N× the configured limit.
2. `RunMigrations` ran unconditionally on server startup with no locking, so
   two replicas starting concurrently could race to apply the same pending
   migration.

v1.7 Epic B ("HA / multi-instance readiness") removes both constraints, plus
closes a third gap ADR 0003 didn't call out explicitly: background jobs
(Keycloak↔DB reconciliation, audit-log retention pruning, analytics
snapshots) assumed exactly one process ever ran them. With more than one
replica, all three would run redundantly — the reconciler and snapshotter
merely waste work, but the audit pruner's `DELETE ... WHERE created_at <
cutoff` executing concurrently on two connections is a real (if narrow) risk
of racing on the `audit_chain_anchors` bookkeeping row.

## Decision

**FreeCloud may now run more than one backend replica against the same
Postgres and a shared Redis.** Four changes make this safe:

### 1. Redis-backed rate limiter (B1)

`internal/middleware/ratelimit_redis.go` adds `RedisRateLimiter`: a
fixed-window counter (`INCR` + `EXPIRE NX` on first increment) keyed
identically to the existing in-memory limiter (host portion of
`RemoteAddr`, `X-Forwarded-For`/`X-Real-IP` never trusted — the same
anti-spoofing rule the in-memory limiter has always enforced). All replicas
sharing one Redis draw from the same counter, so the limit is one budget
for the whole deployment, not per-replica.

- **Selection:** `REDIS_URL` set → Redis-backed; unset → in-memory (the old
  behavior), with a loud startup warning.
- **Production posture:** `config.Validate()` now REQUIRES `REDIS_URL`
  outside `APP_ENV=development|test`. A production deployment can no longer
  silently fall back to per-replica counters just because someone forgot to
  wire Redis.
- **Runtime failure:** if Redis becomes unreachable mid-request, the limiter
  fails CLOSED (denies the request, logs a warning) rather than allowing
  traffic through unthrottled. A rate limiter that opens under load defeats
  its own purpose — the failure mode that most needs protection is exactly
  when Redis is struggling under the same traffic spike.
- **Isolation:** Redis is dedicated to the rate limiter (a distinct
  `redis` service in every compose file) — not shared with Fleet or anything
  else, so its capacity and failure blast radius are scoped to one concern.

### 2. Migrations decoupled from server startup (B2)

The backend binary gained a `migrate` subcommand (`server migrate`) that
applies pending migrations under a dedicated `pg_advisory_lock`
(`migrationLockID`, distinct from the Keycloak-bootstrap lock) and exits.
Bare `server` no longer runs migrations; instead it calls
`db.WaitForSchema`, which either confirms the schema is current or (if
`WAIT_FOR_SCHEMA_TIMEOUT` is set) polls until a concurrently-running
`migrate` job finishes, and fails loudly otherwise.

Every compose file (`docker-compose.localhost.yml`,
`docker-compose.prod.yml`, `docker-compose.e2e.yml`) gained a one-shot
`migrate` service that `backend` depends on via
`condition: service_completed_successfully`. This means:

- Exactly one process runs `RunMigrations` per deploy, by construction (the
  one-shot job), not by hoping N replicas' advisory-lock attempts serialize
  correctly — though `RunMigrations` still takes the lock defensively, so
  even a misconfigured deployment that runs `migrate` twice concurrently is
  safe.
- The turnkey localhost promise (v1.6) is preserved: `docker compose -f
  docker/docker-compose.localhost.yml up` is still one command with zero
  required env vars; `migrate` just runs invisibly first.

### 3. Leader election for background jobs (B3)

New `internal/leader` package: a session-scoped `pg_advisory_lock`, held on
a dedicated connection for as long as an instance is healthy. Losing the
connection (crash, network partition) releases the lock automatically —
Postgres does this, not application code — so another instance's
retry-with-jitter loop picks up leadership with no heartbeat protocol of our
own to get wrong.

`reconcile.Reconciler`, `audit.Pruner`, and `snapshot.Snapshotter` each gained
a `SetLeaderGate(func() bool)` hook, checked at the top of their existing
ticker callback. `nil` gate (the default if never called) means "always
run" — single-instance deployments and any code path that doesn't wire an
Elector behave exactly as before. `main.go` creates one `Elector` per job
with a distinct advisory lock id, so one job's failover is independent of
the others'.

Leadership state is observable: a log line on acquire/release, and a
`freecloud_leader_election_is_leader{job="..."}` Prometheus gauge scraped
from `/metrics`.

**Scoped out:** the SIEM streamer (`internal/siem`) already documents
"single-instance design... at-least-once delivery" as an accepted tradeoff
in its package doc, and duplicate delivery to an external SIEM sink is a
data-quality nuisance, not a correctness/corruption risk the way concurrent
audit pruning would be. It is left ungated. The recurring access-review
scheduler mentioned in the v1.7 Epic B brief does not exist yet as running
code — `review_schedules` has a `next_run_at` column but no Go ticker reads
it — so there was nothing to gate.

### 4. Multi-instance e2e proof (B4)

`docker/docker-compose.e2e-ha.yml` layers a second backend replica
(`backend-e2e-2`) and a Caddy round-robin load balancer (`lb-e2e`) on top of
the base `docker-compose.e2e.yml` stack, without modifying it — the
single-replica e2e path is unaffected. New tests in
`backend/internal/e2e/ha_e2e_test.go` (tag `e2e`, run via `-run TestHA`)
prove, against the real two-replica stack:

- `TestHA_BothReplicasReadyAfterSharedMigration` — both replicas reach
  `/readyz` (live DB query succeeds), which is only possible if the shared
  `migrate-e2e` job's schema is current on both.
- `TestHA_KeycloakBootstrapRanOnce` — both replicas report a provisioned
  realm via `/api/v1/setup/status` with no 5xx, proving the bootstrap
  advisory lock let both start cleanly against one realm.
- `TestHA_RateLimitIsSharedAcrossReplicas` — hammers the LB past the
  30-req/min health-check limiter budget; asserts some requests are
  rejected, which is only possible if the budget is shared (a per-replica
  bug would let ~2x the requests through, matching ADR 0003's original
  concern).
- `TestHA_ExactlyOneReplicaLeadsEachBackgroundJob` — scrapes
  `freecloud_leader_election_is_leader` from both replicas directly and
  asserts, for every job, the two values sum to exactly 1.

This job is wired into `.github/workflows/e2e.yml` as a separate `e2e-ha`
job (dispatched the same way as the existing `e2e` job — `workflow_dispatch`
+ nightly, not on PRs), so the single-replica suite's runtime and stability
are unaffected.

## What still requires a single shared Postgres / Redis

Multi-instance here means multiple **stateless application replicas**
sharing **one** Postgres and **one** Redis — not a fully distributed,
partition-tolerant architecture. Explicitly out of scope:

- **Postgres itself is still a single instance.** No read replicas, no
  failover automation. A Postgres outage takes down every backend replica
  identically to today.
- **Redis itself is still a single instance.** If it goes down, the rate
  limiter fails closed (§1) — availability degrades gracefully (429s) rather
  than corrupting state, but Redis remains a single point of failure for
  request admission.
- **No cross-region / multi-datacenter story.** All replicas are assumed to
  reach the same Postgres and Redis with low latency (the advisory-lock
  session-liveness model degrades under high-latency partitions: a slow but
  not-dead connection could hold a lock past its useful window before the
  retry-with-jitter loop elsewhere notices).
- **The one-shot `migrate` job is still a manual/orchestrator-triggered
  step**, not a rolling zero-downtime schema-migration framework (no
  online-schema-change tooling, no dual-write period for breaking changes).
- **Real infra deploy is unverified.** Like the rest of the v1.x line, this
  is proven in Docker Compose (localhost + e2e), not on a real
  multi-VM/Kubernetes deployment. The e2e HA proof exercises the mechanism,
  not production network conditions (real inter-AZ latency, connection
  pooler behavior under load, etc).

## Consequences

- Production deployments MUST set `REDIS_URL`; `config.Validate()` now
  rejects startup without it. Existing single-instance deployments upgrading
  to this version need to add a Redis service before deploying — this is a
  breaking config change, documented here and in the compose files.
- Operators who want the old zero-Redis single-instance simplicity in
  production no longer have that option; development/test still fall back to
  in-memory automatically.
- The `migrate` step is now a required part of the deploy sequence in
  production and localhost compose (already wired via
  `service_completed_successfully`); a deployment tool that doesn't run
  compose as given (e.g. hand-rolled Kubernetes manifests) MUST replicate the
  one-shot-job-before-server ordering, or set `WAIT_FOR_SCHEMA_TIMEOUT` and
  accept the startup race being resolved by polling instead of ordering.
- Background-job leadership adds one dedicated Postgres connection per job
  per replica (held for the lifetime of the process while healthy) — three
  jobs × N replicas extra connections against the pool's max size. Not
  significant at expected replica counts (2-3) but worth remembering if
  replica count grows much further.
