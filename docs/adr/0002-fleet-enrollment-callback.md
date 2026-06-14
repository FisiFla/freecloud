# ADR 0002: FleetDM enrollment callback

## Status

Accepted (2026-06).

## Context

The offboarding "panic button" wipes a user's devices by looking them up in
`users_devices_mapping`. That table was never populated — onboarding only minted
a Fleet enrollment token and returned it — so offboarding always wiped nothing
while reporting success. A device that enrolls in Fleet needs to be linked back
to the user its token was issued for.

## Decision

Introduce an **enrollment callback** that FleetDM calls when a host enrolls.

- Onboarding persists `(token, user_id, expires_at)` in an `enrollment_tokens`
  table inside the onboarding transaction.
- `POST /api/v1/fleet/enrollment-callback` is **not** JWT-authenticated (Fleet,
  not a browser, calls it). It is authenticated by an **HMAC-SHA256** signature
  over the raw body keyed by `FLEET_WEBHOOK_SECRET`, constant-time compared; an
  unset secret rejects all callbacks (fail closed). It sits outside the JWT auth
  group in the router.
- The handler resolves the token (`404` unknown, `409` already used, `410`
  expired), then in one transaction upserts the device, inserts the
  user↔device mapping, and consumes the token (so it can't be replayed).
- Fleet host IDs are opaque strings, not UUIDs, so `devices.fleet_host_id` and
  `users_devices_mapping.device_id` were widened from `UUID` to `TEXT`
  (migration 003).

## Consequences

- Offboarding now finds and wipes the right hosts with no change to its logic.
- The webhook is a public, unauthenticated-by-JWT surface; its safety rests on
  the HMAC secret and token single-use. Rotating `FLEET_WEBHOOK_SECRET` requires
  updating both the backend and Fleet's webhook config.
- SAML app creation remains a stub (the Keycloak client is created without
  SAML-specific attributes); OIDC apps are fully functional. Closing that is
  tracked separately.
