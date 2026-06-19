# ADR 0003: Single-instance constraint (v1)

## Status

Accepted (2026-06).

## Context

FreeCloud v1 is intentionally scoped to a single backend process. Two
implementation decisions cement this assumption:

1. **In-memory rate limiter** (`internal/middleware/ratelimit.go`). Each
   backend instance maintains its own per-client token-bucket counters. Running
   two instances means each sees only its own share of traffic, so a client can
   exceed the intended rate limit by N× simply by hitting all N instances round-
   robin. There is no shared state between replicas.

2. **Startup migrations without an advisory lock** (`internal/db/schema.go`,
   `RunMigrations`). The migration runner checks `schema_migrations` and applies
   each pending migration inside a transaction. If two backend replicas start
   concurrently against the same database they may both see the same migration as
   pending and race to apply it, which — depending on the migration's SQL — can
   produce duplicate work, constraint violations, or data corruption.

Both properties are known at design time; neither is an oversight. They are
correct, simple choices for a single-instance v1 that would require
non-trivial work to change.

## Decision

**Run exactly one backend instance.** The production Compose stack
(`docker/docker-compose.prod.yml`) does not set `deploy.replicas`; the default
of 1 is intentional. The ops runbook and this ADR make the assumption explicit
so it cannot be violated silently.

## What multi-instance would require

Removing the single-instance constraint requires at minimum:

| Constraint | Required change |
|---|---|
| In-memory rate limiter | Replace with a Redis-backed (or shared-DB-backed) rate limiter so all replicas draw from the same bucket. |
| Startup migrations without an advisory lock | Wrap `RunMigrations` in a `pg_advisory_lock` (or equivalent) so only one replica runs the migration at a time; others wait and then no-op. |
| Stateless HTTP sessions | Auth.js sessions and JWT validation are already stateless (JWTs carry the claim); no session store is needed — this is already satisfied. |

Future work to lift this constraint is tracked as a separate item; it is not
part of v1 scope.

## Consequences

- The production deployment is simple and requires no Redis or external
  coordination service.
- A backend crash or restart clears the in-memory rate-limiter state; counters
  reset to zero. This is acceptable for v1 but means a client that was being
  throttled before the restart can burst immediately after.
- An operator who accidentally runs two backend replicas against the same
  database (e.g. a rolling deploy with `--no-downtime` while the old replica is
  still running) risks a migration race on startup. The startup log line
  `database migrations completed applied_now=N` should be monitored; seeing N>0
  from two overlapping instances is a sign of the race.
- See also ADR 0001 (distributed-state integrity) which notes that the crash
  window between "Keycloak created" and "DB committed" is acceptable for a
  single-instance v1.
