package xyz.fisicaro.freecloud.keycloak;

import jakarta.json.Json;
import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import jakarta.json.JsonReader;
import jakarta.ws.rs.core.Cookie;
import jakarta.ws.rs.core.Response;
import org.keycloak.authentication.AuthenticationFlowContext;
import org.keycloak.authentication.AuthenticationFlowError;
import org.keycloak.authentication.Authenticator;
import org.keycloak.models.KeycloakSession;
import org.keycloak.models.RealmModel;
import org.keycloak.models.UserModel;

import java.io.StringReader;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

public class PostureCheckAuthenticator implements Authenticator {

    private final String evalUrl;
    private final String evalToken;
    private final boolean postureEnabled;
    private final BackendCaller caller;

    /** No-arg constructor used by the factory — reads config from environment. */
    public PostureCheckAuthenticator() {
        this(
            System.getenv("ACCESS_EVAL_URL")   != null ? System.getenv("ACCESS_EVAL_URL")   : "",
            System.getenv("ACCESS_EVAL_TOKEN")  != null ? System.getenv("ACCESS_EVAL_TOKEN")  : "",
            "true".equalsIgnoreCase(System.getenv("POSTURE_CHECK_ENABLED"))
        );
    }

    /** Parametric constructor used by callers who supply only the three core settings. */
    public PostureCheckAuthenticator(String evalUrl, String evalToken, boolean postureEnabled) {
        this(evalUrl, evalToken, postureEnabled, PostureCheckAuthenticator::httpCall);
    }

    /** Full constructor — used by tests to inject a fake BackendCaller. */
    PostureCheckAuthenticator(String evalUrl, String evalToken, boolean postureEnabled, BackendCaller caller) {
        this.evalUrl        = evalUrl   != null ? evalUrl.trim()   : "";
        this.evalToken      = evalToken != null ? evalToken.trim() : "";
        this.postureEnabled = postureEnabled;
        this.caller         = caller;
    }

    /** Default real HTTP implementation used as a method reference. */
    private static BackendResponse httpCall(String url, String token, String body) throws Exception {
        HttpClient client = HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(3))
            .build();

        HttpRequest request = HttpRequest.newBuilder()
            .uri(URI.create(url))
            .timeout(Duration.ofSeconds(3))
            .header("Content-Type", "application/json")
            .header("Authorization", "Bearer " + token)
            .POST(HttpRequest.BodyPublishers.ofString(body))
            .build();

        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
        return new BackendResponse(response.statusCode(), response.body());
    }

    @Override
    public void authenticate(AuthenticationFlowContext context) {
        // Feature flag: disabled → pass through immediately
        if (!postureEnabled) {
            context.success();
            return;
        }

        // Misconfigured with posture enabled → fail closed
        if (evalUrl.isEmpty() || evalToken.isEmpty()) {
            context.failure(AuthenticationFlowError.INTERNAL_ERROR);
            return;
        }

        String userId = context.getUser().getId();

        String deviceId = "";
        Map<String, Cookie> cookies = context.getHttpRequest().getHttpHeaders().getCookies();
        Cookie deviceCookie = cookies.get("freecloud-device-id");
        if (deviceCookie != null && deviceCookie.getValue() != null) {
            deviceId = deviceCookie.getValue();
        }

        String requestBody = Json.createObjectBuilder()
            .add("userId",   userId)
            .add("deviceId", deviceId)
            .build()
            .toString();

        BackendResponse backendResponse;
        try {
            backendResponse = caller.call(evalUrl, evalToken, requestBody);
        } catch (Exception e) {
            // Network error, timeout, etc. — fail closed
            context.failure(AuthenticationFlowError.INTERNAL_ERROR);
            return;
        }

        int status = backendResponse.status();
        if (status < 200 || status >= 300) {
            context.failure(AuthenticationFlowError.ACCESS_DENIED);
            return;
        }

        JsonObject json;
        try (JsonReader reader = Json.createReader(new StringReader(backendResponse.body()))) {
            json = reader.readObject();
        } catch (Exception e) {
            // Unparseable response — fail closed
            context.failure(AuthenticationFlowError.INTERNAL_ERROR);
            return;
        }

        // The backend wraps every response in an envelope: {"success":bool,"data":{...}}.
        // The access-evaluation payload (allow/reasons) lives under "data".
        JsonObject data = null;
        try {
            if (json.containsKey("data")) {
                data = json.getJsonObject("data");
            }
        } catch (Exception e) {
            data = null;
        }
        if (data == null || !data.containsKey("allow")) {
            // Missing/unexpected response shape — fail closed
            context.failure(AuthenticationFlowError.INTERNAL_ERROR);
            return;
        }

        boolean allow = data.getBoolean("allow", false);
        if (allow) {
            context.success();
            return;
        }

        // Denied — extract reasons
        List<String> reasonList = new ArrayList<>();
        if (data.containsKey("reasons")) {
            JsonArray arr = data.getJsonArray("reasons");
            if (arr != null) {
                for (int i = 0; i < arr.size(); i++) {
                    reasonList.add(arr.getString(i));
                }
            }
        }
        String reasonsStr = String.join(", ", reasonList);

        Response challenge = context.form()
            .setAttribute("reasons", reasonsStr)
            .createForm("access-blocked.ftl");
        context.failure(AuthenticationFlowError.ACCESS_DENIED, challenge);
    }

    @Override
    public void action(AuthenticationFlowContext context) {
        // No form submission handling needed
    }

    @Override
    public boolean requiresUser() {
        return true;
    }

    @Override
    public boolean configuredFor(KeycloakSession session, RealmModel realm, UserModel user) {
        return true;
    }

    @Override
    public void setRequiredActions(KeycloakSession session, RealmModel realm, UserModel user) {
        // No required actions to set
    }

    @Override
    public void close() {
        // Nothing to close
    }
}
