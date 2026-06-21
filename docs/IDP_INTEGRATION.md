# IdP Integration Guide — SCIM 2.0 Provisioning

FreeCloud supports SCIM 2.0 (RFC 7643 / RFC 7644) for automated user and group provisioning from external identity providers.

## Overview

SCIM provisioning lets your IdP (Okta, Microsoft Entra, etc.) push user and group changes to FreeCloud automatically — creating accounts on hire, deactivating them on offboard, and keeping group memberships in sync. No manual provisioning is needed.

## Base URL

```
https://<your-freecloud-host>/scim/v2
```

## Authentication

All SCIM write endpoints require a bearer token. Set it via the environment variable:

```
SCIM_BEARER_TOKEN=<random-secret>
```

Generate a strong value:

```bash
openssl rand -hex 32
```

The discovery endpoints (`/ServiceProviderConfig`, `/ResourceTypes`, `/Schemas`) are unauthenticated per RFC 7644 §2 — IdPs call these before they have a token.

---

## Okta SCIM Setup

1. In the Okta Admin Console, open your FreeCloud application → **Provisioning** tab.
2. Click **Configure API Integration** → enable **Enable API Integration**.
3. Set:
   - **SCIM connector base URL**: `https://<your-freecloud-host>/scim/v2`
   - **Unique identifier field**: `userName`
   - **Authentication Mode**: HTTP Header
   - **Authorization**: `Bearer <SCIM_BEARER_TOKEN>`
4. Click **Test API Credentials** — you should see a success response.
5. Under **Provisioning to App**, enable:
   - Create Users
   - Update User Attributes
   - Deactivate Users
   - Push Groups
6. Save. Okta will call `/scim/v2/ServiceProviderConfig` and negotiate capabilities.

## Microsoft Entra (Azure AD) SCIM Setup

1. In Azure Portal, open your FreeCloud **Enterprise Application** → **Provisioning**.
2. Set **Provisioning Mode** to **Automatic**.
3. Under **Admin Credentials**:
   - **Tenant URL**: `https://<your-freecloud-host>/scim/v2`
   - **Secret Token**: `<SCIM_BEARER_TOKEN>`
4. Click **Test Connection** — expect a success banner.
5. Under **Mappings**, verify attribute mappings (see table below).
6. Set **Provisioning Status** to **On** and save.

Entra will sync on its default 40-minute cycle. Trigger an immediate cycle via **Provision on demand** for a specific user.

---

## Supported Operations

| Operation             | Supported | Notes                                      |
|-----------------------|-----------|--------------------------------------------|
| Create user           | Yes       | POST /scim/v2/Users                        |
| Update user           | Yes       | PATCH /scim/v2/Users/{id}                  |
| Deactivate user       | Yes       | PATCH active=false or DELETE               |
| Create group          | Yes       | POST /scim/v2/Groups                       |
| Update group members  | Yes       | PATCH /scim/v2/Groups/{id} add/remove      |
| Delete group          | Yes       | DELETE /scim/v2/Groups/{id}                |
| Bulk operations       | No        | Reported in ServiceProviderConfig          |
| Password push         | No        | FreeCloud delegates auth to Keycloak       |

---

## Attribute Mappings

### Okta → FreeCloud

| Okta attribute   | SCIM attribute          |
|------------------|-------------------------|
| `login`          | `userName`              |
| `email`          | `emails[primary].value` |
| `firstName`      | `name.givenName`        |
| `lastName`       | `name.familyName`       |
| `status`         | `active`                |

### Microsoft Entra → FreeCloud

| Entra attribute              | SCIM attribute          |
|------------------------------|-------------------------|
| `userPrincipalName`          | `userName`              |
| `mail`                       | `emails[primary].value` |
| `givenName`                  | `name.givenName`        |
| `surname`                    | `name.familyName`       |
| `accountEnabled`             | `active`                |

Note: `userPrincipalName` is used as `userName`. Ensure it is a valid email address.

---

## Filter Support

The following SCIM filter operators are supported:

| Operator | Description         | Example                            |
|----------|---------------------|------------------------------------|
| `eq`     | Equals              | `userName eq "alice@example.com"`  |
| `ne`     | Not equals          | `active ne false`                  |
| `co`     | Contains            | `userName co "alice"`              |
| `sw`     | Starts with         | `userName sw "alice"`              |
| `pr`     | Present (non-null)  | `emails pr`                        |
| `and`    | Logical AND         | `userName co "a" and active eq true` |
| `or`     | Logical OR          | `userName eq "a" or userName eq "b"` |

Filters are applied server-side. `startIndex` (1-based) and `count` control pagination.

---

## Discovery Endpoints

These endpoints are public (no bearer token required) and are called automatically by IdPs during setup:

| Endpoint                       | Description                              |
|--------------------------------|------------------------------------------|
| `GET /scim/v2/ServiceProviderConfig` | Reports supported features (patch, filter, etag) |
| `GET /scim/v2/ResourceTypes`   | Lists User and Group resource types      |
| `GET /scim/v2/Schemas`         | Returns full attribute schemas           |

---

## Troubleshooting

| Symptom                          | Cause                                     | Fix                                              |
|----------------------------------|-------------------------------------------|--------------------------------------------------|
| `503 Service Unavailable`        | `SCIM_BEARER_TOKEN` not set               | Set the env var and restart FreeCloud            |
| `401 Unauthorized`               | Wrong or missing bearer token             | Verify the token matches `SCIM_BEARER_TOKEN`     |
| `409 Conflict` on user create    | `userName` already exists                 | IdP is re-provisioning; FreeCloud is idempotent — check IdP config |
| User created but not active      | `active` attribute not mapped             | Add `active` / `accountEnabled` to attribute mapping in IdP |
| Groups not syncing               | Push Groups not enabled in Okta           | Enable **Push Groups** under Provisioning to App |
| Attribute shows `Unknown`        | `givenName`/`familyName` not mapped       | Verify name attribute mappings in IdP            |

For detailed logs, check the FreeCloud container output — each SCIM operation is logged with the actor `scim-provisioner` and an audit record is written to `audit_logs`.
