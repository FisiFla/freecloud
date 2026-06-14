#!/bin/bash
# setup_realm.sh — Idempotent Keycloak realm + groups + client setup
# Requires: curl, jq
# Usage: make kc-setup  OR  bash backend/cmd/scripts/setup_realm.sh

set -euo pipefail

KC_URL="${KEYCLOAK_URL:-http://localhost:8081}"
KC_ADMIN="${KEYCLOAK_ADMIN:-admin}"
KC_ADMIN_PASS="${KEYCLOAK_ADMIN_PASSWORD:-admin}"
REALM="${KEYCLOAK_REALM:-freecloud}"
# Secret for the backend service-account client. Must match KEYCLOAK_CLIENT_SECRET
# used by the Go backend. Generate with: openssl rand -hex 32
SERVICE_CLIENT_SECRET="${KEYCLOAK_CLIENT_SECRET:-dev-only-secret-change-me}"

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

# Grant the service account realm-admin role so it can manage users/clients
SA_USER_ID=$(curl -s "$KC_URL/admin/realms/$REALM/users?username=service-account-$SERVICE_CLIENT_ID" -H "$AUTH" | jq -r '.[0].id')
if [ -n "$SA_USER_ID" ] && [ "$SA_USER_ID" != "null" ]; then
  REALM_MANAGEMENT_UUID=$(curl -s "$KC_URL/admin/realms/$REALM/clients?clientId=realm-management" -H "$AUTH" | jq -r '.[0].id')
  if [ -n "$REALM_MANAGEMENT_UUID" ] && [ "$REALM_MANAGEMENT_UUID" != "null" ]; then
    ADMIN_ROLE=$(curl -s "$KC_URL/admin/realms/$REALM/clients/$REALM_MANAGEMENT_UUID/roles/realm-admin" -H "$AUTH" | jq -c '.')
    if [ -n "$ADMIN_ROLE" ] && [ "$ADMIN_ROLE" != "null" ]; then
      curl -s -X POST "$KC_URL/admin/realms/$REALM/users/$SA_USER_ID/role-mappings/clients/$REALM_MANAGEMENT_UUID" \
        -H "$AUTH" -H "$CT" -d "[$ADMIN_ROLE]" > /dev/null
      echo "  Granted realm-admin to service account."
    fi
  fi
fi

# Create a demo user for testing
echo "→ Ensuring demo user 'demo@freecloud.local'..."
USER_ID=$(curl -s "$KC_URL/admin/realms/$REALM/users?username=demo" -H "$AUTH" | jq -r '.[0].id')
if [ -z "$USER_ID" ] || [ "$USER_ID" = "null" ]; then
  curl -s -X POST "$KC_URL/admin/realms/$REALM/users" -H "$AUTH" -H "$CT" -d "{
    \"username\": \"demo\",
    \"email\": \"demo@freecloud.local\",
    \"firstName\": \"Demo\",
    \"lastName\": \"User\",
    \"enabled\": true,
    \"credentials\": [{\"type\": \"password\", \"value\": \"demo123!\", \"temporary\": false}]
  }" > /dev/null
  echo "  Created (password: demo123!)."
else
  echo "  Already exists."
fi

echo ""
echo "✓ Keycloak realm '$REALM' is ready."
echo "  Admin console: $KC_URL/admin/$REALM/console"
echo "  Demo user: demo@freecloud.local / demo123!"
echo ""
echo "  Backend service-account client 'freecloud-service' secret:"
echo "    $SERVICE_CLIENT_SECRET"
echo "  Set this as KEYCLOAK_CLIENT_SECRET for the Go backend."
