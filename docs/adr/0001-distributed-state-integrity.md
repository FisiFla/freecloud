# ADR 0001: Distributed-state integrity for onboarding/offboarding

## Status

Accepted (2026-06).

## Context

Onboarding and offboarding mutate state across three systems — Keycloak
(identity), FleetDM (devices), and the local PostgreSQL — that cannot share a
transaction. The original implementation ran the steps sequentially and
swallowed failures: a Keycloak user created but not persisted locally became an
unrecoverable orphan, and the endpoint still returned `200 OK`, so monitoring on
HTTP status saw nothing. Offboarding likewise returned `200` even when the
Keycloak account-disable failed.

## Decision

Use **synchronous compensation** (a saga-style rollback), not a transactional
outbox.

- **Onboarding**: an idempotency pre-check (return `409` if the email already
  maps to a user) runs before any external call. After Keycloak creates the
  user, a deferred compensation deletes that user if local persistence fails.
  The user row and its audit-log entry are written in a **single local
  transaction** on a detached context, so a persisted onboarding always has an
  audit record and a client disconnect can't half-persist. Persist failure →
  `500` (which triggers the Keycloak rollback).
- **Offboarding**: the Keycloak account-disable is the critical lock; if it
  fails the endpoint returns **`502`** (not a silent `200`) so callers escalate.
  Session logout and device wipe remain best-effort and are reported in the body.
- **Keycloak** admin tokens are cached until shortly before expiry instead of
  re-authenticating per call.

## Consequences

- Simple, local, testable; no background reconciler or message broker.
- A process crash between "Keycloak created" and "DB committed" can still leave
  an orphan. This window is acceptable for a single-instance v1; the idempotency
  pre-check lets a retry surface the conflict rather than duplicating.
- A transactional outbox + reconciler would close the crash window and is the
  natural next step if/when multi-instance or stricter guarantees are needed.
