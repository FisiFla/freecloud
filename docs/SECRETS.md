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
| `SCIMBearerToken` | `SCIM_BEARER_TOKEN` |
| `AccessEvalToken` | `ACCESS_EVAL_TOKEN` |
| `SMTPPassword` | `SMTP_PASSWORD` |
| `SlackWebhookURL` | `SLACK_WEBHOOK_URL` |
| `WebhookSecret` | `WEBHOOK_SECRET` |
| `SIEMHTTPToken` | `SIEM_HTTP_TOKEN` |

Non-secret fields (URLs, feature flags, timeouts) use the plain env var only.

## Validation

`config.Validate()` remains fail-closed: it rejects empty or insecure-default values for all required secrets in any non-development environment (`APP_ENV` ≠ `development`/`test`). A misconfigured `_FILE` path that returns empty will therefore cause the server to refuse to start, surfacing the misconfiguration immediately rather than running with a missing secret.
