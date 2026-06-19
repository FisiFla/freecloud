# Spike: Keycloak Conditional Access Authenticator SPI

**Status:** Spike / Design Only  
**Epic:** A (Conditional Access) — ticket FCEX2-6  
**Date:** 2026-06-19  
**Author:** FreeCloud Engineering

---

## Problem

The `POST /api/v1/access/evaluate` endpoint (A1) produces an allow/deny posture decision, but nothing enforces that decision during the Keycloak browser login flow. A user can receive a denial from the evaluation service and still obtain a Keycloak session token, because Keycloak does not yet call the evaluation endpoint as part of authentication.

This spike explores how to wire the two together using the **Keycloak Authentication SPI** so that every browser-based login passes a posture gate before Keycloak issues a token.

---

## Background: Keycloak Authenticator SPI

Keycloak's authentication flows are built from pluggable *Authenticators* — Java classes that implement `org.keycloak.authentication.Authenticator`. Each authenticator can:

- inspect the current `AuthenticationFlowContext` (user, session, HTTP request),
- call external services,
- challenge the user (e.g. redirect to a form),
- succeed or fail the flow.

Adding a custom authenticator to the realm's browser flow allows FreeCloud to inject a posture check between username+password and token issuance.

---

## How the Authenticator Would Call `access/evaluate`

At the point the authenticator runs, Keycloak has already authenticated the user's credentials. The authenticator receives an `AuthenticationFlowContext` containing `context.getUser()` (the Keycloak `UserModel`).

Pseudocode flow:

```
authenticate(context):
    user = context.getUser()
    userId = user.getId()   // Keycloak UUID — matches users.keycloak_user_id

    deviceId = resolveDeviceId(context)  // see §3 below

    payload = { "userId": userId, "deviceId": deviceId }
    response = HTTP POST https://freecloud-backend/api/v1/access/evaluate
               Authorization: Bearer <ACCESS_EVAL_TOKEN>
               body: payload

    if response.allow == true:
        context.success()
    else:
        context.failure(
            AuthenticationFlowError.ACCESS_DENIED,
            challenge with redirect to /access-blocked?reasons=<csv>
        )
```

The Java sketch below is illustrative only — no source file is added to the build:

```java
// Illustrative only — not a build artifact
public class PostureCheckAuthenticator implements Authenticator {
    private static final String EVAL_URL = System.getenv("ACCESS_EVAL_URL");
    private static final String EVAL_TOKEN = System.getenv("ACCESS_EVAL_TOKEN");

    @Override
    public void authenticate(AuthenticationFlowContext context) {
        String userId = context.getUser().getId();
        String deviceId = resolveDeviceId(context); // may be null

        try {
            JsonObject body = Json.createObjectBuilder()
                .add("userId", userId)
                .add("deviceId", deviceId != null ? deviceId : "")
                .build();

            HttpResponse<String> resp = HttpClient.newHttpClient().send(
                HttpRequest.newBuilder()
                    .uri(URI.create(EVAL_URL))
                    .header("Authorization", "Bearer " + EVAL_TOKEN)
                    .header("Content-Type", "application/json")
                    .POST(HttpRequest.BodyPublishers.ofString(body.toString()))
                    .timeout(Duration.ofSeconds(3))
                    .build(),
                HttpResponse.BodyHandlers.ofString()
            );

            JsonObject result = Json.createReader(
                new StringReader(resp.body())).readObject();

            if (result.getBoolean("allow", false)) {
                context.success();
            } else {
                String reasons = result.getJsonArray("reasons")
                    .stream()
                    .map(v -> v.toString().replace("\"", ""))
                    .collect(Collectors.joining(","));
                Response challenge = context.form()
                    .setAttribute("reasons", reasons)
                    .createForm("access-blocked.ftl");
                context.failure(AuthenticationFlowError.ACCESS_DENIED, challenge);
            }
        } catch (Exception e) {
            // FAIL CLOSED: any exception → deny
            context.failure(AuthenticationFlowError.INTERNAL_ERROR);
        }
    }

    @Override
    public boolean requiresUser() { return true; }
    @Override
    public boolean configuredFor(KeycloakSession s, RealmModel r, UserModel u) { return true; }
    @Override
    public void setRequiredActions(KeycloakSession s, RealmModel r, UserModel u) {}
    @Override
    public void action(AuthenticationFlowContext context) {}
    @Override
    public void close() {}
}
```

---

## Device Identity: How Does the Device ID Reach Keycloak?

Three options, ranked by recommendation:

### Option A — Enrollment Cookie (Recommended)

During FleetDM enrollment, FreeCloud writes a short-lived, HTTP-only cookie (`freecloud-device-id=<fleet_host_id>`) to the browser. The authenticator reads this cookie from `context.getHttpRequest().getCookie("freecloud-device-id")`.

**Pros:** No browser extension required; works with existing enrollment flow; cookie is scoped to the FreeCloud domain.  
**Cons:** Cookie can be spoofed (attacker can present any device ID). Mitigated by the FleetDM posture check — a spoofed ID would fail posture unless the real device is also compliant.

### Option B — HTTP Header (Device Agent)

A lightweight device agent (or browser extension) injects an `X-Device-ID` header on requests to the Keycloak domain.

**Pros:** Harder to spoof than a cookie (requires code on device).  
**Cons:** Requires distributing and managing a browser extension or agent; complex MDM deployment.

### Option C — Step-Up MFA Style

No device ID at login; after token issuance, the backend evaluates posture on the first API call and redirects to `/access-blocked` if the device is non-compliant.

**Pros:** No changes to Keycloak flow.  
**Cons:** A compliant token is issued before posture is checked; window for abuse; worse UX.

**Recommendation:** Option A for the first iteration. Document the spoofability caveat and mitigate with short cookie TTL (15 minutes). Option B should be revisited once a device agent exists.

---

## Packaging the JAR into the Keycloak Image

The authenticator is compiled into a fat JAR and deployed by:

1. Adding a `Dockerfile.keycloak` that extends the official Keycloak image:

```dockerfile
FROM quay.io/keycloak/keycloak:25
COPY freecloud-posture-authenticator.jar /opt/keycloak/providers/
RUN /opt/keycloak/bin/kc.sh build
```

2. The `setup_realm.sh` script already runs against the Keycloak admin API after container start. Extend it to add the authenticator to the browser flow:

```bash
# Add to setup_realm.sh — illustrative
KC_ADMIN_API="$KEYCLOAK_URL/admin/realms/$KC_REALM"

# Get existing browser flow ID
FLOW_ID=$(curl -sf -H "Authorization: Bearer $TOKEN" \
  "$KC_ADMIN_API/authentication/flows" | jq -r '.[] | select(.alias=="browser") | .id')

# Add the posture execution to the browser flow after username+password
curl -sf -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"provider":"freecloud-posture-check"}' \
  "$KC_ADMIN_API/authentication/flows/$FLOW_ID/executions/execution"
```

The `ACCESS_EVAL_URL` and `ACCESS_EVAL_TOKEN` env vars are passed to the Keycloak container. The authenticator reads them via `System.getenv()`.

---

## Fail-Closed Behavior

Every error path in the authenticator must deny, not allow:

| Scenario | Behavior |
|---|---|
| `access/evaluate` unreachable (timeout/network error) | `context.failure(INTERNAL_ERROR)` |
| Backend returns non-2xx | `context.failure(ACCESS_DENIED)` |
| `allow=false` | `context.failure(ACCESS_DENIED)` |
| `allow` field missing | deny (treat as false) |
| Cookie absent / device not enrolled | backend returns deny → `ACCESS_DENIED` |

The 3-second HTTP timeout on the outbound call is load-bearing: Keycloak's request has its own timeout; exceeding it with a long external call would break all logins.

---

## Effort Estimate

| Task | Estimate |
|---|---|
| Java authenticator SPI implementation | 2–3 days |
| Keycloak Dockerfile + provider build | 0.5 day |
| `setup_realm.sh` extension + flow wiring | 0.5 day |
| Device enrollment cookie in FleetDM callback | 1 day |
| Integration tests (Keycloak container + real flow) | 2 days |
| Documentation + runbook | 0.5 day |
| **Total** | **~6.5 days** |

---

## Recommended Approach for the Real Build

1. Implement Option A (enrollment cookie) in the FleetDM callback handler first — zero Keycloak changes required.
2. Build the JAR authenticator against Keycloak 25 SPI. Run unit tests with `keycloak-core` on the classpath only (no running Keycloak needed for unit tests).
3. Add a `docker-compose.test.keycloak.yml` that spins up a real Keycloak container for integration tests; wire it into CI with a `test-kc` Makefile target (separate from the fast `verify` gate).
4. Gate the Keycloak image rebuild behind a `kc-build` Makefile target so developers who only touch the Go backend don't rebuild it on every run.
5. Ship behind a feature flag (`POSTURE_CHECK_ENABLED=true`) so the flow can be disabled in dev without removing the authenticator from the flow configuration.
