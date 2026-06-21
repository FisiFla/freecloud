# FreeCloud API Reference

Full REST API reference, auth model, and environment variable table.

## Auth Model

### User-facing endpoints (JWT Bearer)

All `/api/v1/` endpoints (except the ones listed under "Unauthenticated" below)
require a JWT Bearer token issued by Keycloak:

```
Authorization: Bearer <access_token>
```

The frontend obtains the access token via Auth.js (Keycloak OIDC) and publishes it
into the API client module (`frontend/src/lib/api.ts`). Every request picks it up
automatically.

The backend validates:
- JWT signature (against Keycloak's JWKS endpoint).
- `iss` (issuer) — must match `KEYCLOAK_URL/realms/KEYCLOAK_REALM`.
- `aud` (audience) — must match `KEYCLOAK_AUDIENCE`.

### API token fallback (service accounts)

Machine clients can authenticate with a long-lived API token instead of a JWT:

```
Authorization: Bearer fc_<token>
```

API tokens are created via `POST /api/v1/api-tokens` (super-admin only). The
backend resolves the token from the database and maps it to a set of permissions
before passing it through the same permission-check middleware.

### SCIM provisioning (dedicated bearer token)

SCIM endpoints (`/scim/v2/*`) use a separate `SCIM_BEARER_TOKEN`. This must be a
high-entropy secret (e.g. `openssl rand -base64 33`).

### Access evaluation (dedicated bearer token)

`POST /api/v1/access/evaluate` uses `ACCESS_EVAL_TOKEN`. Called by the Keycloak
Authenticator SPI (or any service that needs to gate SSO on device posture).

### Fleet enrollment callback (HMAC)

`POST /api/v1/fleet/enrollment-callback` is **not** JWT-authenticated. It is
authenticated by `X-Fleet-Signature: sha256=<hex HMAC-SHA256(raw_body, FLEET_WEBHOOK_SECRET)>`,
constant-time compared. An unset secret rejects all callbacks.

## Response Envelope

All `/api/v1/` responses use a standard JSON envelope:

```json
{ "success": true, "data": <payload> }
{ "success": false, "error": "human-readable message" }
{ "success": false, "errors": [{ "field": "email", "message": "invalid email" }] }
```

## Unauthenticated Endpoints

| Method | Endpoint | Description |
|---|---|---|
| GET | `/healthz` | Liveness probe — always `200` if the process is up |
| GET | `/readyz` | Readiness probe — `200` when DB + Keycloak are reachable |
| GET | `/metrics` | Prometheus metrics scrape target |
| POST | `/api/v1/fleet/enrollment-callback` | FleetDM enrollment webhook (HMAC-authenticated) |
| POST | `/api/v1/access/evaluate` | Posture check for conditional access SPI (`ACCESS_EVAL_TOKEN`) |
| POST | `/api/v1/auth/forgot-password` | Public password-reset trigger (rate-limited 10/min) |
| GET | `/scim/v2/Users[/{id}]` | SCIM 2.0 user operations (`SCIM_BEARER_TOKEN`) |
| POST/PATCH/DELETE | `/scim/v2/Users/{id}` | SCIM 2.0 user operations |
| GET/POST/PATCH/DELETE | `/scim/v2/Groups[/{id}]` | SCIM 2.0 group operations |

## API Endpoints (JWT-authenticated)

### Health

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/health` | — | Simple `{ status: "ok" }` |
| GET | `/api/v1/health/keycloak` | — (rate-limited) | Ping Keycloak |
| GET | `/api/v1/health/fleetdm` | — (rate-limited) | Ping FleetDM |

### Onboarding & Offboarding

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| POST | `/api/v1/onboard` | `onboard_offboard` | Create user in Keycloak + FleetDM enrollment token |
| POST | `/api/v1/onboard/bulk` | `onboard_offboard` | Bulk CSV onboarding (multipart/form-data, `file` field) |
| POST | `/api/v1/offboard/{userId}` | `onboard_offboard` | Disable account, terminate sessions, wipe devices |

**Onboard request:**
```json
{
  "firstName": "Jane",
  "lastName": "Doe",
  "email": "jane@example.com",
  "department": "Engineering",
  "role": "engineer"
}
```

**Onboard response:**
```json
{
  "user": { "id": "...", "email": "...", "username": "..." },
  "enrollmentToken": "abc123",
  "enrollmentURL": "https://fleet.example.com/enroll?token=abc123",
  "warning": "(optional)",
  "nextStep": "(optional)"
}
```

**Offboard response:**
```json
{
  "userId": "...",
  "sessionsTerminated": true,
  "sessionTerminationError": "(optional)",
  "devicesWiped": 2,
  "devicesFailed": 0,
  "warnings": []
}
```

**Bulk onboard** — upload a CSV with headers `firstName,lastName,email,department,role`.
Returns `207` on partial success:
```json
{
  "total": 5, "succeeded": 4, "skipped": 1, "failed": 0,
  "results": [{ "row": 1, "email": "...", "status": "succeeded" }, ...]
}
```

### Users

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/users` | `read_users` | List all users |
| GET | `/api/v1/users/{id}` | `read_users` | Get single user with linked devices |
| PATCH | `/api/v1/users/{id}` | `manage_users` | Update profile fields |
| POST | `/api/v1/users/{id}/reset-password` | `manage_users` | Send Keycloak reset-password email |
| GET | `/api/v1/users/{id}/mfa-status` | `read_users` | OTP/WebAuthn status |
| POST | `/api/v1/users/{id}/require-mfa` | `manage_mfa` | Enforce MFA type (`totp` or `webauthn`) |

**User object:**
```json
{
  "id": "...",
  "keycloakUserId": "...",
  "email": "jane@example.com",
  "firstName": "Jane",
  "lastName": "Doe",
  "department": "Engineering",
  "role": "engineer",
  "disabled": false,
  "createdAt": "2026-01-01T00:00:00Z",
  "devices": [...]
}
```

**PATCH user request** (all fields optional):
```json
{ "firstName": "Jane", "lastName": "Doe", "department": "...", "role": "...", "enabled": true }
```

### Groups & Roles

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/groups` | `read_groups` | List Keycloak groups |
| POST | `/api/v1/groups` | `manage_groups` | Create group |
| POST | `/api/v1/users/{id}/groups` | `manage_groups` | Assign user to group |
| DELETE | `/api/v1/users/{id}/groups/{groupId}` | `manage_groups` | Remove user from group |
| GET | `/api/v1/roles` | `read_groups` | List Keycloak realm roles |
| POST | `/api/v1/users/{id}/roles` | `manage_users` | Assign realm role to user |

### Applications (SSO)

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/apps` | `read_apps` | List registered apps |
| POST | `/api/v1/apps/create` | `manage_apps` | Create OIDC or SAML app |
| POST | `/api/v1/apps/{appId}/assign` | `manage_apps` | Assign user to app |
| GET | `/api/v1/apps/{appId}/policy` | `read_apps` | Get per-app conditional access policy |
| PUT | `/api/v1/apps/{appId}/policy` | `manage_policies` | Create/update access policy |

**Create app request:**
```json
{
  "name": "My App",
  "protocol": "OIDC",
  "redirectURIs": ["https://myapp.example.com/callback"],
  "baseURL": "https://myapp.example.com"
}
```

**App access policy (PUT body):**
```json
{
  "requireEnrolled": true,
  "requireDiskEncrypted": true,
  "requireNoCriticalVulns": false,
  "maxOsAgeDays": 90
}
```

### Device Management

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| POST | `/api/v1/auth/device-check` | `self_service` | Check calling user's device posture |
| POST | `/api/v1/devices/{id}/lock` | `manage_devices` | Remote lock a device |
| GET | `/api/v1/users/{id}/devices/software` | `read_compliance` | Software inventory for user's devices |
| GET | `/api/v1/users/{id}/devices/compliance` | `read_compliance` | Posture per device for a user |
| GET | `/api/v1/compliance` | `read_compliance` | Org-wide compliance summary |

**Device check response:**
```json
{ "passed": true, "failures": [] }
```

### Fleet Teams & Policies

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/teams` | `read_compliance` | List Fleet teams |
| POST | `/api/v1/teams` | `manage_policies` | Create Fleet team |
| POST | `/api/v1/teams/{id}/policies` | `manage_policies` | Assign Fleet policy to team |
| POST | `/api/v1/teams/{id}/hosts` | `manage_devices` | Move hosts to team |
| GET | `/api/v1/policies` | `read_compliance` | List Fleet policies |

### Audit Log

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/audit-logs` | `read_audit_logs` | List audit entries (filterable) |
| GET | `/api/v1/audit-logs/export` | `export_audit_logs` | Download CSV or JSON (`?format=csv|json`) |

**Query parameters for listing:** `actor`, `action`, `limit`, `offset`.

**Audit log entry:**
```json
{
  "id": "...",
  "actorId": "admin",
  "action": "onboard",
  "targetType": "user",
  "targetId": "...",
  "details": { "email": "jane@example.com" },
  "createdAt": "2026-01-01T00:00:00Z"
}
```

### API Tokens

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/api-tokens` | `manage_api_tokens` | List tokens (token value never returned) |
| POST | `/api/v1/api-tokens` | `manage_api_tokens` | Create token (token returned once only) |
| DELETE | `/api/v1/api-tokens/{id}` | `manage_api_tokens` | Revoke token |

**Create token request:**
```json
{ "name": "CI pipeline", "role": "reader", "serviceIdentity": "ci", "expiresInDays": 90 }
```

### Access Review Campaigns

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/campaigns` | `review_campaigns` | List campaigns |
| POST | `/api/v1/campaigns` | `manage_campaigns` | Create campaign |
| GET | `/api/v1/campaigns/{id}/items` | `review_campaigns` | List items in a campaign |
| POST | `/api/v1/campaigns/{id}/items/{itemId}/decide` | `review_campaigns` | Approve or revoke an item |
| POST | `/api/v1/campaigns/{id}/complete` | `manage_campaigns` | Complete a campaign |

### Self-Service Portal

All portal endpoints require the `self_service` permission (all authenticated users).

| Method | Endpoint | Description |
|---|---|---|
| GET | `/api/v1/portal/me/devices` | My enrolled devices |
| GET | `/api/v1/portal/me/apps` | My assigned apps |
| GET | `/api/v1/portal/me/compliance` | My device compliance summary |
| POST | `/api/v1/portal/access-requests` | Request access to an app |
| GET | `/api/v1/portal/access-requests` | List pending access requests (admin, `manage_users`) |
| PATCH | `/api/v1/portal/access-requests/{id}` | Approve or reject a request (admin, `manage_users`) |

### Analytics

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/analytics/snapshots` | `read_compliance` | Time-series KPI snapshots (`?limit=N`) |

**Snapshot row:**
```json
{
  "id": 1,
  "capturedAt": "2026-01-01T00:00:00Z",
  "complianceRate": 0.94,
  "enrolledDevices": 47,
  "mfaCoveragePct": 0.88,
  "appCount": 12,
  "onboardCount": 3,
  "offboardCount": 1
}
```

### Admin

| Method | Endpoint | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/admin/drift` | `manage_users` | Keycloak↔DB reconciliation drift report |

### Fleet Enrollment Callback (HMAC-authenticated)

```
POST /api/v1/fleet/enrollment-callback
X-Fleet-Signature: sha256=<hex HMAC-SHA256(raw_body, FLEET_WEBHOOK_SECRET)>
Content-Type: application/json

{"enrollment_token": "...", "host_id": "...", "hostname": "...", "os_version": "..."}
```

Responses: `200` success, `400` bad body, `401` bad signature, `404` unknown token,
`409` token already consumed, `410` token expired.

### SCIM 2.0

| Method | Endpoint | Description |
|---|---|---|
| GET | `/scim/v2/Users` | List users (supports `filter`, `startIndex`, `count`) |
| POST | `/scim/v2/Users` | Create user |
| GET | `/scim/v2/Users/{id}` | Get user |
| PATCH | `/scim/v2/Users/{id}` | Update user (RFC 7644 patch operations) |
| DELETE | `/scim/v2/Users/{id}` | Delete user |
| GET | `/scim/v2/Groups` | List groups |
| POST | `/scim/v2/Groups` | Create group |
| GET | `/scim/v2/Groups/{id}` | Get group |
| PATCH | `/scim/v2/Groups/{id}` | Update group members |
| DELETE | `/scim/v2/Groups/{id}` | Delete group |

Authentication: `Authorization: Bearer <SCIM_BEARER_TOKEN>`.

---

## Environment Variable Reference

### Backend

| Variable | Default | Required in prod | Description |
|---|---|---|---|
| `APP_ENV` | `""` (treated as production) | — | Set to `development` to disable fail-closed checks locally |
| `PORT` | `8080` | — | HTTP listen port |
| `DATABASE_URL` | `postgres://freecloud:freecloud@localhost:5432/freecloud?sslmode=disable` | Yes | PostgreSQL DSN — must use `sslmode=require` or stronger in production |
| `KEYCLOAK_URL` | `http://localhost:8081` | Yes | Keycloak base URL |
| `KEYCLOAK_REALM` | `freecloud` | — | Keycloak realm name |
| `KEYCLOAK_CLIENT_ID` | `admin-cli` | Yes | Confidential service-account client (not `admin-cli`) |
| `KEYCLOAK_CLIENT_SECRET` | `""` | Yes | Service-account client secret |
| `KEYCLOAK_AUDIENCE` | `freecloud-dashboard` | Yes | Expected JWT `aud` claim |
| `FLEET_URL` | `http://localhost:8082` | Yes | FleetDM base URL |
| `FLEET_API_TOKEN` | `""` | Yes | FleetDM API token |
| `FLEET_WEBHOOK_SECRET` | `""` | Yes | HMAC key for enrollment callback |
| `SCIM_BEARER_TOKEN` | `""` | Yes | Bearer token for SCIM provisioning |
| `ACCESS_EVAL_TOKEN` | `""` | Yes | Bearer token for access evaluation |
| `CORS_ORIGIN` | `http://localhost:3000` | Yes | Allowed CORS origin |
| `RECONCILE_INTERVAL` | `15m` | — | Keycloak↔DB reconciliation job interval (`0` to disable) |
| `SNAPSHOT_INTERVAL` | `1h` | — | Analytics snapshot job interval (`0` to disable) |
| `SIEM_SYSLOG_ADDR` | `""` | — | SIEM syslog target (`host:port`) |
| `SIEM_SYSLOG_NET` | `udp` | — | Syslog network (`udp` or `tcp`) |
| `SIEM_HTTP_URL` | `""` | — | SIEM HTTP endpoint URL |
| `SIEM_HTTP_TOKEN` | `""` | — | SIEM HTTP bearer token |
| `SIEM_INTERVAL` | `5s` | — | SIEM streaming poll interval |
| `NOTIFY_EMAIL` | `false` | — | Enable email event notifications |
| `SMTP_HOST` | `""` | — | SMTP server hostname |
| `SMTP_PORT` | `587` | — | SMTP server port |
| `SMTP_FROM` | `""` | — | Sender email address |
| `SMTP_TO` | `""` | — | Comma-separated recipient addresses |
| `SMTP_PASSWORD` | `""` | — | SMTP password |
| `NOTIFY_SLACK` | `false` | — | Enable Slack event notifications |
| `SLACK_WEBHOOK_URL` | `""` | — | Slack incoming webhook URL |
| `NOTIFY_WEBHOOK` | `false` | — | Enable generic webhook event notifications |
| `WEBHOOK_URL` | `""` | — | Webhook endpoint URL |
| `WEBHOOK_SECRET` | `""` | — | Webhook signing secret |
| `NOTIFY_EVENT_OFFBOARD` | `true` | — | Notify on offboard events |
| `NOTIFY_EVENT_DRIFT` | `true` | — | Notify on reconciliation drift |
| `NOTIFY_EVENT_COMPLIANCE` | `true` | — | Notify on compliance-failure events |
| `ALLOW_ACTOR_HEADER` | `""` | — | Set to `true` to allow `X-Actor-ID` header (dev/test only) |

### Frontend

| Variable | Default | Description |
|---|---|---|
| `NEXT_PUBLIC_API_URL` | `http://localhost:8080` | Backend API base URL (baked at build time; must be `https://` in production) |
| `AUTH_SECRET` | — | Auth.js session-signing secret (required in production) |
| `AUTH_KEYCLOAK_ID` | — | OIDC client ID for the frontend (`freecloud-dashboard`) |
| `AUTH_KEYCLOAK_SECRET` | — | OIDC client secret for the frontend |

### Production (Caddy / Compose)

| Variable | Description |
|---|---|
| `DASHBOARD_DOMAIN` | Public hostname for the Next.js dashboard |
| `API_DOMAIN` | Public hostname for the Go backend API |
| `KC_DOMAIN` | Public hostname for Keycloak |
| `DASHBOARD_PUBLIC_URL` | Full `https://` URL for the dashboard |
| `API_PUBLIC_URL` | Full `https://` URL for the API (used as `NEXT_PUBLIC_API_URL` at build time) |
| `KC_PUBLIC_URL` | Full `https://` URL for Keycloak |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | Postgres credentials |
| `KEYCLOAK_ADMIN` / `KEYCLOAK_ADMIN_PASSWORD` | Keycloak bootstrap admin |

Generate secrets with: `openssl rand -base64 33`
