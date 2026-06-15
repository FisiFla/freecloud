#!/bin/bash
# setup_realm.sh — Idempotent Keycloak realm + groups + client setup
# Requires: curl, jq
# Usage: make kc-setup  OR  bash backend/cmd/scripts/setup_realm.sh
#
# DEVELOPMENT/BOOTSTRAP ONLY. It refuses to run outside the development
# environment unless ALLOW_DEV_SETUP=true is set explicitly.

set -euo pipefail

APP_ENV_VALUE="${APP_ENV:-development}"

# Refuse to run in non-development environments unless the operator explicitly
# acknowledges they are bootstrapping a real realm. Demo users remain blocked by
# a separate guard below.
if [ "$APP_ENV_VALUE" != "development" ] && [ "${ALLOW_DEV_SETUP:-}" != "true" ]; then
  echo "ERROR: setup_realm.sh is for development/bootstrap only (APP_ENV=${APP_ENV:-<unset>})." >&2
  echo "       To bootstrap a non-development realm, set ALLOW_DEV_SETUP=true and CREATE_DEMO_USER=false." >&2
  exit 1
fi

KC_URL="${KEYCLOAK_URL:-http://localhost:8081}"
KC_ADMIN="${KEYCLOAK_ADMIN:-admin}"
KC_ADMIN_PASS="${KEYCLOAK_ADMIN_PASSWORD:-admin}"
REALM="${KEYCLOAK_REALM:-freecloud}"

# Generate a random secret if KEYCLOAK_CLIENT_SECRET is unset, so dev setups
# don't share a well-known value. Requires openssl (or /dev/urandom fallback).
gen_random_hex() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  elif [ -r /dev/urandom ]; then
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  else
    echo "" # caller handles the empty case
  fi
}

SERVICE_SECRET_PROVIDED=true
if [ -z "${KEYCLOAK_CLIENT_SECRET:-}" ]; then
  SERVICE_SECRET_PROVIDED=false
  SERVICE_CLIENT_SECRET="$(gen_random_hex)"
  if [ -z "$SERVICE_CLIENT_SECRET" ]; then
    echo "ERROR: KEYCLOAK_CLIENT_SECRET is unset and neither openssl nor /dev/urandom is available to generate one." >&2
    exit 1
  fi
else
  SERVICE_CLIENT_SECRET="$KEYCLOAK_CLIENT_SECRET"
fi

# Demo user toggle. Defaults to true only for local development. Production and
# staging bootstraps must opt in explicitly, and the guard below blocks that
# unless ALLOW_DEMO_USER_OUTSIDE_DEV=true is also set.
if [ -z "${CREATE_DEMO_USER:-}" ]; then
  if [ "$APP_ENV_VALUE" = "development" ]; then
    CREATE_DEMO_USER=true
  else
    CREATE_DEMO_USER=false
  fi
fi
if [ "$APP_ENV_VALUE" != "development" ] && [ "$CREATE_DEMO_USER" = "true" ] && [ "${ALLOW_DEMO_USER_OUTSIDE_DEV:-}" != "true" ]; then
  echo "ERROR: refusing to create a demo user outside development." >&2
  echo "       Set CREATE_DEMO_USER=false for production/bootstrap runs." >&2
  exit 1
fi
# Demo user password. Override with DEMO_PASSWORD; generated if unset.
DEMO_PASSWORD_PROVIDED=true
if [ -z "${DEMO_PASSWORD:-}" ]; then
  DEMO_PASSWORD_PROVIDED=false
  DEMO_PASSWORD="$(gen_random_hex | cut -c1-16)"
  if [ -z "$DEMO_PASSWORD" ]; then
    DEMO_PASSWORD="change-me-$(date +%s)"
  fi
fi

# Get admin token
echo "→ Authenticating as admin..."
TOKEN=$(curl -s -X POST "$KC_URL/realms/master/protocol/openid-connect/token" \
  -d "client_id=admin-cli" \
  -d "username=$KC_ADMIN" \
  -d "password=$KC_ADMIN_PASS" \
  -d "grant_type=password" | jq -r '.access_token')

if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
  echo "ERROR: Failed to get admin token. Is Keycloak running at $KC_URL?"
  exit 1
fi

AUTH="Authorization: Bearer $TOKEN"
CT="Content-Type: application/json"

# Create realm if it doesn't exist
echo "→ Checking realm '$REALM'..."
if curl -s -o /dev/null -w "%{http_code}" "$KC_URL/admin/realms/$REALM" -H "$AUTH" | grep -q 404; then
  echo "→ Creating realm '$REALM'..."
  curl -s -X POST "$KC_URL/admin/realms" -H "$AUTH" -H "$CT" -d "{
    \"realm\": \"$REALM\",
    \"enabled\": true,
    \"displayName\": \"FreeCloud\",
    \"loginWithEmailAllowed\": true,
    \"duplicateEmailsAllowed\": false,
    \"resetPasswordAllowed\": true,
    \"editUsernameAllowed\": true,
    \"registrationAllowed\": false
  }" > /dev/null
  echo "  Realm created."
else
  echo "  Realm already exists."
fi

# Create department groups
GROUPS=("Engineering" "Marketing" "Sales" "Operations")
for group in "${GROUPS[@]}"; do
  echo "→ Ensuring group '$group'..."
  EXISTING=$(curl -s "$KC_URL/admin/realms/$REALM/groups?search=$group" -H "$AUTH" | jq -r '.[0].id')
  if [ -z "$EXISTING" ] || [ "$EXISTING" = "null" ]; then
    curl -s -X POST "$KC_URL/admin/realms/$REALM/groups" -H "$AUTH" -H "$CT" \
      -d "{\"name\": \"$group\"}" > /dev/null
    echo "  Created."
  else
    echo "  Already exists."
  fi
done

# Create a confidential client for backend-to-KC communication.
# We deliberately use a custom clientId ("freecloud-service") instead of
# reusing Keycloak's reserved "admin-cli" name to avoid confusion with the
# built-in public client.
SERVICE_CLIENT_ID="freecloud-service"
echo "→ Ensuring '$SERVICE_CLIENT_ID' client with a service account..."
CLIENT_UUID=$(curl -s "$KC_URL/admin/realms/$REALM/clients?clientId=$SERVICE_CLIENT_ID" -H "$AUTH" | jq -r '.[0].id')
if [ -z "$CLIENT_UUID" ] || [ "$CLIENT_UUID" = "null" ]; then
  curl -s -X POST "$KC_URL/admin/realms/$REALM/clients" -H "$AUTH" -H "$CT" -d "{
    \"clientId\": \"$SERVICE_CLIENT_ID\",
    \"name\": \"FreeCloud Backend Service\",
    \"enabled\": true,
    \"publicClient\": false,
    \"serviceAccountsEnabled\": true,
    \"authorizationServicesEnabled\": false,
    \"standardFlowEnabled\": false,
    \"directAccessGrantsEnabled\": false,
    \"secret\": \"$SERVICE_CLIENT_SECRET\"
  }" > /dev/null
  # Re-fetch the generated UUID now that it exists
  CLIENT_UUID=$(curl -s "$KC_URL/admin/realms/$REALM/clients?clientId=$SERVICE_CLIENT_ID" -H "$AUTH" | jq -r '.[0].id')
  echo "  Created."
else
  # Ensure the secret matches what we want (idempotent update)
  curl -s -X PUT "$KC_URL/admin/realms/$REALM/clients/$CLIENT_UUID" -H "$AUTH" -H "$CT" \
    -d "{\"secret\": \"$SERVICE_CLIENT_SECRET\"}" > /dev/null
  echo "  Already exists (secret synced)."
fi

# Grant the service account ONLY the realm-management roles it actually needs
# (manage-users + manage-clients) instead of the realm-admin super-role. Least
# privilege: a leaked service-account secret then cannot take over the realm.
SA_USER_ID=$(curl -s "$KC_URL/admin/realms/$REALM/users?username=service-account-$SERVICE_CLIENT_ID" -H "$AUTH" | jq -r '.[0].id')
if [ -n "$SA_USER_ID" ] && [ "$SA_USER_ID" != "null" ]; then
  REALM_MANAGEMENT_UUID=$(curl -s "$KC_URL/admin/realms/$REALM/clients?clientId=realm-management" -H "$AUTH" | jq -r '.[0].id')
  if [ -n "$REALM_MANAGEMENT_UUID" ] && [ "$REALM_MANAGEMENT_UUID" != "null" ]; then
    ROLES_JSON="[]"
    for role in manage-users manage-clients; do
      ROLE_OBJ=$(curl -s "$KC_URL/admin/realms/$REALM/clients/$REALM_MANAGEMENT_UUID/roles/$role" -H "$AUTH" | jq -c '.')
      if [ -n "$ROLE_OBJ" ] && [ "$ROLE_OBJ" != "null" ]; then
        ROLES_JSON=$(echo "$ROLES_JSON" | jq -c ". + [$ROLE_OBJ]")
      fi
    done
    if [ "$ROLES_JSON" != "[]" ]; then
      curl -s -X POST "$KC_URL/admin/realms/$REALM/users/$SA_USER_ID/role-mappings/clients/$REALM_MANAGEMENT_UUID" \
        -H "$AUTH" -H "$CT" -d "$ROLES_JSON" > /dev/null
      echo "  Granted manage-users + manage-clients to service account."
    fi
  fi
fi

# Create a demo user for testing (skip with CREATE_DEMO_USER=false)
if [ "$CREATE_DEMO_USER" = "true" ]; then
  echo "→ Ensuring demo user 'demo@freecloud.local'..."
  USER_ID=$(curl -s "$KC_URL/admin/realms/$REALM/users?username=demo" -H "$AUTH" | jq -r '.[0].id')
  if [ -z "$USER_ID" ] || [ "$USER_ID" = "null" ]; then
    curl -s -X POST "$KC_URL/admin/realms/$REALM/users" -H "$AUTH" -H "$CT" -d "{
      \"username\": \"demo\",
      \"email\": \"demo@freecloud.local\",
      \"firstName\": \"Demo\",
      \"lastName\": \"User\",
      \"enabled\": true,
      \"credentials\": [{\"type\": \"password\", \"value\": \"$DEMO_PASSWORD\", \"temporary\": false}]
    }" > /dev/null
    echo "  Created."
  else
    echo "  Already exists."
  fi
fi

echo ""
echo "✓ Keycloak realm '$REALM' is ready."
echo "  Admin console: $KC_URL/admin/$REALM/console"

if [ "$CREATE_DEMO_USER" = "true" ]; then
  if [ "$DEMO_PASSWORD_PROVIDED" = "true" ]; then
    echo "  Demo user: demo@freecloud.local (password: set via DEMO_PASSWORD)"
  else
    # Password was generated this run — print it once so the dev can log in.
    echo "  Demo user: demo@freecloud.local / $DEMO_PASSWORD  (auto-generated; set DEMO_PASSWORD to override)"
  fi
else
  echo "  Demo user: skipped (CREATE_DEMO_USER=false)"
fi

echo ""
echo "  ┌─ DEV ONLY ────────────────────────────────────────────────────┐"
echo "  │ The service-account secret is printed below. Never use this  │"
echo "  │ output in staging/production. Rotate before any real deploy. │"
echo "  └──────────────────────────────────────────────────────────────┘"
if [ "$SERVICE_SECRET_PROVIDED" = "true" ]; then
  echo "  Backend service-account client 'freecloud-service' secret:"
  echo "    (provided via KEYCLOAK_CLIENT_SECRET — not reprinted here)"
else
  echo "  Backend service-account client 'freecloud-service' secret (auto-generated):"
  echo "    $SERVICE_CLIENT_SECRET"
fi
echo "  Set this as KEYCLOAK_CLIENT_SECRET for the Go backend."
