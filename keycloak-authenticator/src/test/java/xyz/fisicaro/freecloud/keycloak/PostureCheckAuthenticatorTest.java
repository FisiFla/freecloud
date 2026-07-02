package xyz.fisicaro.freecloud.keycloak;

import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import org.keycloak.authentication.AuthenticationFlowContext;
import org.keycloak.authentication.AuthenticationFlowError;
import org.keycloak.common.ClientConnection;
import org.keycloak.forms.login.LoginFormsProvider;
import org.keycloak.models.ClientModel;
import org.keycloak.models.UserModel;
import org.keycloak.sessions.AuthenticationSessionModel;

import jakarta.ws.rs.core.HttpHeaders;
import jakarta.ws.rs.core.Cookie;
import jakarta.ws.rs.core.Response;

import java.util.Collections;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class PostureCheckAuthenticatorTest {

    @Mock AuthenticationFlowContext context;
    @Mock UserModel user;
    @Mock LoginFormsProvider forms;
    @Mock Response response;
    @Mock org.keycloak.http.HttpRequest httpRequest;
    @Mock HttpHeaders httpHeaders;
    @Mock AuthenticationSessionModel authSession;
    @Mock ClientModel client;
    @Mock ClientConnection connection;

    @BeforeEach
    void setUp() {
        lenient().when(context.getUser()).thenReturn(user);
        lenient().when(user.getId()).thenReturn("user-123");
        lenient().when(context.getHttpRequest()).thenReturn(httpRequest);
        lenient().when(httpRequest.getHttpHeaders()).thenReturn(httpHeaders);
        lenient().when(httpHeaders.getCookies()).thenReturn(Collections.emptyMap());
        lenient().when(context.form()).thenReturn(forms);
        lenient().when(forms.setAttribute(any(), any())).thenReturn(forms);
        lenient().when(forms.createForm(any())).thenReturn(response);
        lenient().when(context.getConnection()).thenReturn(connection);
        lenient().when(connection.getRemoteAddr()).thenReturn("10.0.0.5");
    }

    @Test
    void testDisabledByFeatureFlag() {
        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://eval.example.com", "token", false
        );
        auth.authenticate(context);
        verify(context).success();
        verify(context, never()).failure(any(), any());
        verify(context, never()).failure(any(AuthenticationFlowError.class));
    }

    @Test
    void testMissingConfigFailsClosed_emptyUrl() {
        PostureCheckAuthenticator auth = new PostureCheckAuthenticator("", "token", true);
        auth.authenticate(context);
        verify(context).failure(AuthenticationFlowError.INTERNAL_ERROR);
        verify(context, never()).success();
    }

    @Test
    void testMissingConfigFailsClosed_emptyToken() {
        PostureCheckAuthenticator auth = new PostureCheckAuthenticator("http://example.com", "", true);
        auth.authenticate(context);
        verify(context).failure(AuthenticationFlowError.INTERNAL_ERROR);
        verify(context, never()).success();
    }

    @Test
    void testAllowResponse() {
        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://localhost:9999/no-server", "token", true,
            (url, token, body) -> new BackendResponse(200, "{\"success\":true,\"data\":{\"allow\":true}}")
        );
        auth.authenticate(context);
        verify(context).success();
        verify(context, never()).failure(any(), any());
    }

    @Test
    void testSendsClientAndDeviceContext() {
        when(context.getAuthenticationSession()).thenReturn(authSession);
        when(authSession.getClient()).thenReturn(client);
        when(client.getId()).thenReturn("kc-client-uuid");
        when(httpHeaders.getCookies()).thenReturn(Map.of(
            "freecloud-device-id", new Cookie("freecloud-device-id", "host-123")
        ));
        AtomicReference<String> capturedBody = new AtomicReference<>("");

        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://localhost:9999/no-server", "token", true,
            (url, token, body) -> {
                capturedBody.set(body);
                return new BackendResponse(200, "{\"success\":true,\"data\":{\"allow\":true}}");
            }
        );
        auth.authenticate(context);

        verify(context).success();
        assertTrue(capturedBody.get().contains("\"userId\":\"user-123\""));
        assertTrue(capturedBody.get().contains("\"appId\":\"kc-client-uuid\""));
        assertTrue(capturedBody.get().contains("\"deviceId\":\"host-123\""));
    }

    @Test
    void testDenyResponse() {
        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://localhost:9999/no-server", "token", true,
            (url, token, body) -> new BackendResponse(200, "{\"success\":true,\"data\":{\"allow\":false,\"reasons\":[\"firewall disabled\"]}}")
        );
        auth.authenticate(context);
        verify(context).failure(eq(AuthenticationFlowError.ACCESS_DENIED), any(Response.class));
        verify(context, never()).success();
    }

    @Test
    void testNetworkError() {
        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://localhost:9999/no-server", "token", true,
            (url, token, body) -> { throw new RuntimeException("connection refused"); }
        );
        auth.authenticate(context);
        verify(context).failure(AuthenticationFlowError.INTERNAL_ERROR);
        verify(context, never()).success();
    }

    @Test
    void testNon2xxResponse() {
        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://localhost:9999/no-server", "token", true,
            (url, token, body) -> new BackendResponse(500, "internal server error")
        );
        auth.authenticate(context);
        verify(context).failure(AuthenticationFlowError.ACCESS_DENIED);
        verify(context, never()).success();
    }

    // ---- A3: client-IP forwarding ----

    @Test
    void testClientIp_TrustProxyOff_UsesDirectPeerAddress_IgnoresSpoofedXFF() {
        when(httpHeaders.getHeaderString("X-Forwarded-For")).thenReturn("6.6.6.6");
        AtomicReference<String> capturedBody = new AtomicReference<>("");

        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://localhost:9999/no-server", "token", true, false,
            (url, token, body) -> {
                capturedBody.set(body);
                return new BackendResponse(200, "{\"success\":true,\"data\":{\"allow\":true}}");
            }
        );
        auth.authenticate(context);

        verify(context).success();
        // TRUST_PROXY=false: the spoofed XFF header must be completely ignored,
        // even though it was present — only the direct peer address is sent.
        assertTrue(capturedBody.get().contains("\"clientIp\":\"10.0.0.5\""));
        assertTrue(!capturedBody.get().contains("6.6.6.6"));
    }

    @Test
    void testClientIp_TrustProxyOn_UsesRightmostForwardedForHop() {
        // Attacker-forged leftmost entry + the real trusted-proxy-appended
        // rightmost entry — only the rightmost must be used.
        when(httpHeaders.getHeaderString("X-Forwarded-For")).thenReturn("6.6.6.6, 203.0.113.9");
        AtomicReference<String> capturedBody = new AtomicReference<>("");

        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://localhost:9999/no-server", "token", true, true,
            (url, token, body) -> {
                capturedBody.set(body);
                return new BackendResponse(200, "{\"success\":true,\"data\":{\"allow\":true}}");
            }
        );
        auth.authenticate(context);

        verify(context).success();
        assertTrue(capturedBody.get().contains("\"clientIp\":\"203.0.113.9\""));
        assertTrue(!capturedBody.get().contains("6.6.6.6"));
    }

    @Test
    void testClientIp_TrustProxyOn_NoHeaderFallsBackToDirectPeer() {
        when(httpHeaders.getHeaderString("X-Forwarded-For")).thenReturn(null);
        AtomicReference<String> capturedBody = new AtomicReference<>("");

        PostureCheckAuthenticator auth = new PostureCheckAuthenticator(
            "http://localhost:9999/no-server", "token", true, true,
            (url, token, body) -> {
                capturedBody.set(body);
                return new BackendResponse(200, "{\"success\":true,\"data\":{\"allow\":true}}");
            }
        );
        auth.authenticate(context);

        verify(context).success();
        assertTrue(capturedBody.get().contains("\"clientIp\":\"10.0.0.5\""));
    }
}
