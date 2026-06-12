#!/bin/bash
# setup_realm.sh — Idempotent Keycloak realm + groups + client setup
# Requires: curl, jq
# Usage: make kc-setup  OR  bash backend/cmd/scripts/setup_realm.sh

set -euo pipefail

KC_URL="${KEYCLOAK_URL:-http://localhost:8081}"
KC_ADMIN="${KEYCLOAK_ADMIN:-admin}"
KC_ADMIN_PASS="${KEYCLOAK_ADMIN_PASSWORD:-admin}"
REALM="${KEYCLOAK_REALM:-freecloud}"

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

# Create admin-cli client with client credentials (for backend-to-KC communication)
echo "→ Ensuring 'admin-cli' client..."
CLIENT_ID=$(curl -s "$KC_URL/admin/realms/$REALM/clients?clientId=admin-cli" -H "$AUTH" | jq -r '.[0].id')
if [ -z "$CLIENT_ID" ] || [ "$CLIENT_ID" = "null" ]; then
  curl -s -X POST "$KC_URL/admin/realms/$REALM/clients" -H "$AUTH" -H "$CT" -d "{
    \"clientId\": \"admin-cli\",
    \"name\": \"FreeCloud Admin CLI\",
    \"enabled\": true,
    \"publicClient\": false,
    \"serviceAccountsEnabled\": true,
    \"authorizationServicesEnabled\": false,
    \"standardFlowEnabled\": false,
    \"directAccessGrantsEnabled\": false
  }" > /dev/null
  echo "  Created."
else
  echo "  Already exists."
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
