# External Secrets (C3 / FCEX3-15)

FreeCloud resolves secret values through three providers, checked in order:

## Precedence

```
<VAR>_FILE  >  <VAR>_VAULT_PATH  >  <VAR> (plain env)
```

### 1. File (`<VAR>_FILE`)

Set `<VAR>_FILE` to the path of a file containing the secret value (one line, trailing newline stripped). This is the standard Docker / Kubernetes secrets-volume convention.

```bash
# Docker Compose
KEYCLOAK_CLIENT_SECRET_FILE=/run/secrets/kc_secret

# Kubernetes
# Mount a Secret as a volume, then set:
FLEET_API_TOKEN_FILE=/var/secrets/fleet-token
```

A missing or unreadable file returns an empty value, which `Validate()` rejects in production (fail-closed).

#### How the production stack delivers secrets

`docker/init-secrets/generate.sh` writes every auto-generated secret twice: into
`.secrets/secrets.env` (`KEY=VALUE`) and into `.secrets/files/<key>` (one file
per secret). The production compose mounts the per-key files as Docker secrets
(`/run/secrets/<name>`) and points the services at them:

| Secret | Service | Delivery |
|---|---|---|
| `POSTGRES_PASSWORD` | postgres | `POSTGRES_PASSWORD_FILE` (native to the Postgres image) |
| `KC_ADMIN_PASSWORD` | keycloak | `FREECLOUD_KEYCLOAK_ADMIN_PASSWORD_FILE` → read by `docker/keycloak-entrypoint.sh` |
| `POSTGRES_PASSWORD` | keycloak | `FREECLOUD_KEYCLOAK_DB_PASSWORD_FILE` → read by `docker/keycloak-entrypoint.sh` |
| `ACCESS_EVAL_TOKEN` | keycloak | `FREECLOUD_ACCESS_EVAL_TOKEN_FILE` → read by `docker/keycloak-entrypoint.sh` |
| `AUTH_SECRET` | frontend | mounted file, read by the container entrypoint |
| `SCIM_BEARER_TOKEN`, `ACCESS_EVAL_TOKEN`, `FLEET_WEBHOOK_SECRET`, `KC_ADMIN_PASSWORD` | backend | `<VAR>_FILE` (resolved natively by `config.resolveSecret`) |
| `PROVISIONING_MASTER_KEY` | backend | `<VAR>_FILE` **and** the plain env var — `backend/internal/handlers/provisioning.go` still reads the plain variable directly |

The values are therefore not visible in `docker inspect` or
`/proc/<pid>/environ` — with the single exception of the database password,
which has to appear inside the `DATABASE_URL` that Compose interpolates, and
of the operator-provided variables in `.env.prod` (`FLEET_API_TOKEN`,
`AUTH_KEYCLOAK_SECRET`, `SMTP_PASSWORD`).

`.secrets/` is created with mode `0700`; the per-key files inside are
world-readable so that containers running as a non-root uid can read them,
and the directory permissions are what keep them off the host.

### 2. HashiCorp Vault (`<VAR>_VAULT_PATH`)

Set `<VAR>_VAULT_PATH` to the path of a KV v1 or v2 secret, and also set `VAULT_ADDR` and `VAULT_TOKEN`.

The secret must have a key named `value`:
```
# KV v2
vault kv put secret/freecloud/kc_secret value="my-secret"
KEYCLOAK_CLIENT_SECRET_VAULT_PATH=secret/data/freecloud/kc_secret
VAULT_ADDR=https://vault.internal
VAULT_TOKEN=s.abc123
```

If the Vault read fails (unreachable, bad token, missing `value` field), the provider falls through to the plain env var. In production, `Validate()` will then reject an empty plain env var.

### 3. Plain environment variable (`<VAR>`)

The existing behaviour: set the env var directly. Still fully supported and the simplest option for non-containerised deploys.

## Secret fields

The following config fields use `resolveSecret`:

| Config field | Env var |
|---|---|
| `DatabaseURL` | `DATABASE_URL` |
| `KeycloakClientSecret` | `KEYCLOAK_CLIENT_SECRET` |
| `FleetAPIToken` | `FLEET_API_TOKEN` |
| `FleetWebhookSecret` | `FLEET_WEBHOOK_SECRET` |
| `DeviceCookieSecret` | `DEVICE_COOKIE_SECRET` (optional; falls back to fleet webhook secret) |
| `ProvisioningMasterKey` | `PROVISIONING_MASTER_KEY` |
| `SCIMBearerToken` | `SCIM_BEARER_TOKEN` |
| `AccessEvalToken` | `ACCESS_EVAL_TOKEN` |

### Device identity cookie signing

| Config field | Env var | Required |
|---|---|---|
| `DeviceCookieSecret` | `DEVICE_COOKIE_SECRET` | Optional |

`POST /api/v1/enrollment/device-identity` and `POST /api/v1/access/evaluate`
HMAC-sign/verify `freecloud-device-id` with:

1. **`DEVICE_COOKIE_SECRET`** when set (preferred for independent rotation), else
2. **`FLEET_WEBHOOK_SECRET`** (default, so existing deploys need no new secret).

Rotating the active key invalidates outstanding device cookies (users re-mint
before login). If you only use the fleet webhook secret, rotation also requires
updating Fleet's webhook signature configuration at the same time.
| `SMTPPassword` | `SMTP_PASSWORD` |
| `SlackWebhookURL` | `SLACK_WEBHOOK_URL` |
| `WebhookSecret` | `WEBHOOK_SECRET` |
| `SIEMHTTPToken` | `SIEM_HTTP_TOKEN` |

Non-secret fields (URLs, feature flags, timeouts) use the plain env var only.

## Validation

`config.Validate()` remains fail-closed: it rejects empty or insecure-default values for all required secrets in any non-development environment (`APP_ENV` ≠ `development`/`test`). A misconfigured `_FILE` path that returns empty will therefore cause the server to refuse to start, surfacing the misconfiguration immediately rather than running with a missing secret.
