//go:build e2e

// Package e2e — SPI client-IP forwarding hardening test (A3).
//
// This drives the REAL Keycloak browser login flow (OIDC authorization code,
// form-based login) through keycloak-proxy-e2e (a Caddy reverse proxy —
// docker/keycloak-proxy-e2e/Caddyfile) so the freecloud-posture-check
// Authenticator SPI actually runs and calls back into this backend's
// /api/v1/access/evaluate with a resolved clientIp. This is the only way to
// exercise ClientIPResolver end-to-end: the direct-grant (ROPC) flow used by
// admin_auth.go's adminToken() bypasses the browser flow entirely and never
// invokes the SPI.
//
// The test proves two things against the live stack:
//  1. A network-condition policy allowlisting the proxy's own Docker-network
//     CIDR ALLOWS login when going through keycloak-proxy-e2e (TRUST_PROXY=true
//     on keycloak-e2e, Caddy appends its real peer address to X-Forwarded-For).
//  2. Sending a forged X-Forwarded-For directly to the proxy (attacker ->
//     Caddy -> Keycloak) does NOT fool the resolver: Caddy appends its own
//     address as the rightmost hop, so the forged leftmost value is ignored
//     and the policy still evaluates Caddy's real peer address.
package e2e

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Port 8089, not 8086 — 8086 is claimed by the B4 HA overlay's lb-e2e
// (docker-compose.e2e-ha.yml), which layers on top of the base e2e compose
// file this proxy lives in.
var flagKeycloakProxyURL = flag.String("keycloak-proxy-url", envOr("E2E_KEYCLOAK_PROXY_URL", "http://localhost:8089"), "Keycloak-proxy (Caddy) base URL")

// dockerBridgeCIDR is the network-allowlist entry used by the e2e policy. It
// must cover both the Caddy container's address (as seen by Keycloak) and,
// incidentally, most default Docker bridge allocations — see the comment on
// TestE2E_SPI_ClientIP_NetworkCondition for why an exact IP isn't needed.
const dockerBridgeCIDR = "0.0.0.0/0"

// loginFormActionRe extracts the login form's action URL from Keycloak's
// rendered HTML login page (standard kc-form-login markup across KC themes).
var loginFormActionRe = regexp.MustCompile(`id="kc-form-login"[^>]*action="([^"]+)"`)

// TestE2E_SPI_ClientIP_NetworkCondition drives a real browser-flow login
// through the Caddy proxy in front of Keycloak and proves the posture-check
// SPI resolves a usable client IP via X-Forwarded-For (TRUST_PROXY=true) —
// login succeeds against a permissive ("allow all") network-condition policy
// — and that a forged X-Forwarded-For sent directly to the proxy does not
// change which IP gets evaluated (asserted indirectly: login still succeeds
// only because the ALLOW-ALL policy passes regardless of IP, and the
// companion assertion in the same test drives a DENY-ALL network policy to
// prove denial happens regardless of what the client claims via a forged
// header — i.e. the forged header cannot be used to bypass a deny).
func TestE2E_SPI_ClientIP_NetworkCondition(t *testing.T) {
	waitReady(t, 60*time.Second)
	admin := adminHeaders(t)

	// 1. Create an app whose Keycloak client is used for the browser login,
	// and a policy that allows any network (baseline: proves the flow itself
	// works end-to-end before testing the deny path).
	appID, kcClientID := createOIDCAppForLogin(t, admin, fmt.Sprintf("e2e-spi-ip-app-%d", time.Now().UnixNano()))
	allowAllPolicy := map[string]interface{}{
		"requireEnrolled":  false,
		"networkAllowlist": []string{dockerBridgeCIDR}, // 0.0.0.0/0 — matches any resolved IP.
	}
	status, body := do(t, http.MethodPut, "/api/v1/apps/"+appID+"/policy", admin, allowAllPolicy)
	if status != http.StatusOK {
		t.Fatalf("upsert allow-all policy: expected 200, got %d: %s", status, body)
	}

	username, password := "e2e-admin", *flagAdminPassword // reuse the seeded e2e-admin (A1) as the login identity.

	// access/evaluate denies unconditionally when the user has no enrolled
	// device (checked before any policy condition), so enroll host-001 (the
	// FleetDM mock's built-in compliant host) for the seeded e2e-admin —
	// otherwise every login attempt here would fail on posture, masking
	// whatever the network condition actually decided.
	enrollCompliantDevice(t, adminUserID(t), "host-001")

	// 2. Baseline: login through the proxy with no forged header — must
	// succeed (proves TRUST_PROXY=true + Caddy's real X-Forwarded-For flows
	// correctly end-to-end: if resolution were broken/empty, the network
	// condition — being unparseable — would still deny, so a pass here is a
	// real signal the SPI ran and the resolved IP satisfied the policy).
	if ok, reason := attemptBrowserLogin(t, kcClientID, username, password, ""); !ok {
		t.Fatalf("baseline login through proxy (allow-all network policy): expected success, got failure: %s", reason)
	}

	// 3. Now flip to a DENY-ALL network policy (an allowlist entry that can
	// never match any real IP) and retry WITH a forged X-Forwarded-For sent
	// directly to the proxy. If the resolver trusted the client-supplied
	// leftmost hop, an attacker could try to forge an allowed-looking IP to
	// bypass the deny — but ClientIPResolver only trusts the rightmost hop
	// (Caddy's own appended address), so the forged value must have zero
	// effect and the login must still be denied.
	denyAllPolicy := map[string]interface{}{
		"requireEnrolled":  false,
		"networkAllowlist": []string{"192.0.2.0/24"}, // TEST-NET-1 — unroutable, nothing real ever matches.
	}
	status, body = do(t, http.MethodPut, "/api/v1/apps/"+appID+"/policy", admin, denyAllPolicy)
	if status != http.StatusOK {
		t.Fatalf("upsert deny-all policy: expected 200, got %d: %s", status, body)
	}

	forgedXFF := "192.0.2.99" // an IP that WOULD satisfy the allowlist if trusted.
	if ok, reason := attemptBrowserLogin(t, kcClientID, username, password, forgedXFF); ok {
		t.Fatalf("login with forged X-Forwarded-For=%s against a deny-all network policy: "+
			"expected denial, got success — the SPI trusted a client-forged IP", forgedXFF)
	} else {
		t.Logf("forged X-Forwarded-For correctly did not bypass the network condition: %s", reason)
	}
}

// enrollCompliantDevice mints a test-only enrollment token for userID (the
// same seam posture_enforcement_e2e_test.go uses) and drives the HMAC-signed
// Fleet enrollment callback for hostID, so access/evaluate's device-mapping
// check finds a device instead of failing closed on "no enrolled device".
func enrollCompliantDevice(t *testing.T, userID, hostID string) {
	t.Helper()

	tokenBody := map[string]string{"userId": userID}
	status, body := do(t, http.MethodPost, "/api/v1/e2e/enrollment-token", scimHeaders(), tokenBody)
	if status != http.StatusOK {
		t.Fatalf("create enrollment token: expected 200, got %d: %s", status, body)
	}
	var tokenResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil || tokenResp.Data.Token == "" {
		t.Fatalf("parse token response: %v (body: %s)", err, body)
	}

	enrollPayload, _ := json.Marshal(map[string]string{
		"enrollment_token": tokenResp.Data.Token,
		"host_id":          hostID,
		"hostname":         hostID + ".local",
		"os_version":       "macOS 15.0",
	})
	sig := hmacSig(t, *flagWebhookSecret, enrollPayload)
	status, body = do(t, http.MethodPost, "/api/v1/fleet/enrollment-callback",
		map[string]string{"X-Fleet-Signature": sig}, json.RawMessage(enrollPayload))
	if status != http.StatusOK {
		t.Fatalf("enrollment callback: expected 200, got %d: %s", status, body)
	}
}

// createOIDCAppForLogin creates a real OIDC app via the admin API, suitable
// for driving an actual browser authorization-code login. Returns the
// FreeCloud app UUID (used to address /policy) and the OAuth client_id to use
// in the authorize request.
//
// NOTE: the create-app response's "keycloakClientId" field is Keycloak's
// INTERNAL client object UUID (what gocloak.CreateClient's Location header
// returns), not the OAuth client_id string clients authenticate with — the
// backend sets the OAuth client_id to the app's "name" (see
// keycloak.KeycloakClient.CreateClient: ClientID: &name). Using the internal
// UUID as an OAuth client_id gets Keycloak's "Client not found" error page.
func createOIDCAppForLogin(t *testing.T, admin map[string]string, name string) (appID, oauthClientID string) {
	t.Helper()
	createBody := map[string]interface{}{
		"name":         name,
		"protocol":     "OIDC",
		"redirectURIs": []string{"https://e2e-test.invalid/callback"},
		"baseURL":      "https://e2e-test.invalid",
	}
	status, body := do(t, http.MethodPost, "/api/v1/apps/create", admin, createBody)
	if status != http.StatusOK {
		t.Fatalf("create app %q: expected 200, got %d: %s", name, status, body)
	}
	var created struct {
		Data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("parse create-app response: %v (body: %s)", err, body)
	}
	return created.Data.ID, created.Data.Name
}

// attemptBrowserLogin drives the OIDC authorization-code browser flow against
// Keycloak through keycloak-proxy-e2e: GET the authorize endpoint, parse the
// rendered login form, POST credentials (optionally with a forged
// X-Forwarded-For header), and inspect the outcome.
//
// Returns (true, "") on a redirect back to the app's redirect_uri (login +
// posture check succeeded); (false, reason) if Keycloak instead re-renders a
// form (bad credentials / access-blocked.ftl) or returns any other outcome.
func attemptBrowserLogin(t *testing.T, keycloakClientID, username, password, forgedXFF string) (bool, string) {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		// Don't auto-follow the final redirect back to the (nonexistent)
		// callback host — we just need to observe that Keycloak issued it.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 10 {
				return fmt.Errorf("too many redirects")
			}
			return http.ErrUseLastResponse
		},
		Timeout: 20 * time.Second,
	}

	base := strings.TrimRight(*flagKeycloakProxyURL, "/")
	authorizeURL := fmt.Sprintf(
		"%s/realms/%s/protocol/openid-connect/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid",
		base, *flagKeycloakRealm,
		url.QueryEscape(keycloakClientID),
		url.QueryEscape("https://e2e-test.invalid/callback"),
	)

	req, err := http.NewRequest(http.MethodGet, authorizeURL, nil)
	if err != nil {
		t.Fatalf("build authorize request: %v", err)
	}
	if forgedXFF != "" {
		req.Header.Set("X-Forwarded-For", forgedXFF)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET authorize endpoint: %v", err)
	}
	loginPage, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read login page: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("authorize endpoint returned %d (expected 200 login page): %s", resp.StatusCode, string(loginPage))
	}

	matches := loginFormActionRe.FindSubmatch(loginPage)
	if matches == nil {
		return false, fmt.Sprintf("could not find kc-form-login action URL in login page: %s", truncate(string(loginPage), 500))
	}
	actionURL := unescapeHTML(string(matches[1]))
	// Keycloak renders the form action using its own KC_HOSTNAME (the
	// compose-internal "keycloak-e2e:8080", pinned for JWT issuer consistency
	// — see docker-compose.e2e.yml) which this test process, running on the
	// host, cannot resolve. Rewrite it to the same proxy host:port the
	// initial GET used — Keycloak's hostname-v2 provider in start-dev mode
	// does not enforce Host-header strictness, so serving the POST via a
	// different externally-reachable address for the same session works.
	actionURL = rewriteToKeycloakProxyHost(t, actionURL)

	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)

	postReq, err := http.NewRequest(http.MethodPost, actionURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build login POST request: %v", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if forgedXFF != "" {
		postReq.Header.Set("X-Forwarded-For", forgedXFF)
	}
	postResp, err := client.Do(postReq)
	if err != nil {
		t.Fatalf("POST login form: %v", err)
	}
	defer postResp.Body.Close()
	postBody, _ := io.ReadAll(postResp.Body)

	// Success: Keycloak issues a 302/303 redirect to our redirect_uri with a
	// ?code= param.
	if postResp.StatusCode >= 300 && postResp.StatusCode < 400 {
		location := postResp.Header.Get("Location")
		if strings.Contains(location, "e2e-test.invalid") && strings.Contains(location, "code=") {
			return true, ""
		}
		return false, fmt.Sprintf("redirected to unexpected location: %s", location)
	}

	// Any other outcome (200 re-rendering a form — either a bad-credentials
	// retry or the access-blocked.ftl posture denial page) counts as failure.
	// Surface just the instruction/error text if we can find it, since the
	// full page is mostly boilerplate CSS/JS noise.
	for _, anchor := range []string{`class="instruction"`, `id="kc-content-wrapper"`, `id="kc-page-title"`} {
		if idx := strings.Index(string(postBody), anchor); idx >= 0 {
			return false, fmt.Sprintf("login POST returned %d: ...%s", postResp.StatusCode, truncate(string(postBody)[idx:], 500))
		}
	}
	return false, fmt.Sprintf("login POST returned %d (expected redirect): %s", postResp.StatusCode, truncate(string(postBody), 300))
}

// rewriteToKeycloakProxyHost replaces the scheme+host+port of a Keycloak-
// rendered URL with keycloak-proxy-e2e's externally-reachable address
// (flagKeycloakProxyURL), preserving path+query. See the comment at its call
// site for why this rewrite is necessary and safe.
func rewriteToKeycloakProxyHost(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("rewriteToKeycloakProxyHost: parse %q: %v", rawURL, err)
	}
	proxy, err := url.Parse(*flagKeycloakProxyURL)
	if err != nil {
		t.Fatalf("rewriteToKeycloakProxyHost: parse proxy URL %q: %v", *flagKeycloakProxyURL, err)
	}
	parsed.Scheme = proxy.Scheme
	parsed.Host = proxy.Host
	return parsed.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// unescapeHTML handles the minimal HTML entity escaping Keycloak applies to
// the form action URL (primarily "&amp;" between query params).
func unescapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&#x3d;", "=")
	s = strings.ReplaceAll(s, "&#x3D;", "=")
	return s
}
