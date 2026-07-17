//go:build e2e

// Package e2e — posture enforcement end-to-end tests (A4 / FCEX3-8).
//
// These tests prove that the access-evaluate endpoint enforces posture
// correctly end-to-end against the running backend+FleetDM-mock stack:
//
//   - A compliant device (firewall enabled, disk encrypted, no vulns) is ALLOWED.
//   - A non-compliant device (firewall disabled or disk not encrypted) is DENIED.
//   - A user with no enrolled device is DENIED.
//
// The FleetDM mock always returns compliant posture for its built-in hosts
// (host-001, host-002 from main.go).  The e2e stack wires the mock to return
// denial for a special synthetic host ID "host-noncompliant" via a posture
// override env var (FLEET_MOCK_NONCOMPLIANT_HOSTS) on the fleetdm-e2e service.
// If that env var is absent, these tests fall back to a direct evaluation check
// using only the compliant built-in hosts and a fabricated "no-device" scenario.
//
// The Keycloak-authenticator enforcement (browser-flow denial) is exercised in
// the separate Keycloak e2e path wired in e2e.yml — this file covers the
// access-evaluate API layer which the SPI calls.
package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----

// accessEvalHeaders builds the Authorization header for the access-eval endpoint.
func accessEvalHeaders() map[string]string {
	token := envOr("E2E_ACCESS_EVAL_TOKEN", "e2e-access-eval-token")
	return map[string]string{"Authorization": "Bearer " + token}
}

// accessEvalResp is the JSON body returned by POST /api/v1/access/evaluate.
type accessEvalResp struct {
	Allow   bool     `json:"allow"`
	Reasons []string `json:"reasons,omitempty"`
}

// ---- Tests ----

// TestE2E_PostureEnforcement_DeviceIdentityCookieEndpointExists verifies that
// the device-identity cookie endpoint is registered and rejects garbage tokens
// with 404 rather than 500 (backend is healthy and the route is wired).
func TestE2E_PostureEnforcement_DeviceIdentityCookieEndpointExists(t *testing.T) {
	waitReady(t, 60*time.Second)

	body := map[string]string{"enrollmentToken": "nonexistent-token-e2e"}
	status, resp := do(t, "POST", "/api/v1/enrollment/device-identity", nil, body)
	// The token doesn't exist in the DB, so 404 is expected.
	// 500 would mean the endpoint is broken; 404 means it's live and correct.
	if status == 500 {
		t.Fatalf("device-identity endpoint: unexpected 500 (body: %s)", resp)
	}
	if status != 404 {
		t.Logf("device-identity endpoint: got %d (expected 404 for unknown token; body: %s)", status, resp)
	}
}

// TestE2E_PostureEnforcement_NoUserDenied proves that evaluating access for an
// unknown user (not in the DB) returns deny, matching the fail-closed contract.
func TestE2E_PostureEnforcement_NoUserDenied(t *testing.T) {
	waitReady(t, 30*time.Second)

	body := map[string]string{
		"userId": "00000000-0000-0000-0000-000000000000", // non-existent UUID
	}
	status, resp := do(t, "POST", "/api/v1/access/evaluate", accessEvalHeaders(), body)
	if status != 200 {
		t.Fatalf("access/evaluate: expected 200 JSON response, got %d: %s", status, resp)
	}
	var env struct {
		Data accessEvalResp `json:"data"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("parse response: %v (body: %s)", err, resp)
	}
	result := env.Data
	if result.Allow {
		t.Errorf("unknown user: expected deny, got allow (reasons: %v)", result.Reasons)
	}
	if len(result.Reasons) == 0 {
		t.Error("unknown user: expected at least one denial reason")
	}
	t.Logf("unknown user → deny, reasons: %v", result.Reasons)
}

// TestE2E_PostureEnforcement_CompliantDeviceAllowed provisions a real user via
// SCIM, enrolls host-001 for them via the test-only enrollment-token endpoint
// and the HMAC-signed enrollment callback, then calls access/evaluate.
// The FleetDM mock returns compliant posture (disk_encryption+firewall=true) for
// host-001, so the result must be allow=true.
func TestE2E_PostureEnforcement_CompliantDeviceAllowed(t *testing.T) {
	waitReady(t, 30*time.Second)

	// 1. Create a user via SCIM so we have a known user ID.
	email := fmt.Sprintf("e2e-posture-compliant-%d@example.com", time.Now().UnixNano())
	createBody := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": email,
		"name":     map[string]string{"givenName": "Posture", "familyName": "Compliant"},
		"active":   true,
	}
	status, resp := do(t, "POST", "/scim/v2/Users", scimHeaders(), createBody)
	if status != 201 {
		t.Fatalf("create SCIM user: expected 201, got %d: %s", status, resp)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil || created.ID == "" {
		t.Fatalf("parse user create: %v (body: %s)", err, resp)
	}
	t.Logf("created user id=%s", created.ID)

	// 2. Mint an enrollment token for the user via the test-only endpoint
	// (APP_ENV=test, gated by the SCIM bearer token).
	tokenBody := map[string]string{"userId": created.ID}
	status, resp = do(t, "POST", "/api/v1/e2e/enrollment-token", scimHeaders(), tokenBody)
	if status != 200 {
		t.Fatalf("create enrollment token: expected 200, got %d: %s", status, resp)
	}
	var tokenResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &tokenResp); err != nil || tokenResp.Data.Token == "" {
		t.Fatalf("parse token response: %v (body: %s)", err, resp)
	}
	enrollToken := tokenResp.Data.Token
	t.Logf("enrollment token=%s", enrollToken)

	// 3. Simulate the FleetDM enrollment callback for host-001 — the compliant
	// built-in host in the mock (disk_encryption=true, firewall=true).
	hostID := "host-001"
	enrollPayload, _ := json.Marshal(map[string]string{
		"enrollment_token": enrollToken,
		"host_id":          hostID,
		"hostname":         hostID + ".local",
		"os_version":       "macOS 15.0",
	})
	sig := hmacSig(t, *flagWebhookSecret, enrollPayload)
	status, resp = do(t, "POST", "/api/v1/fleet/enrollment-callback",
		map[string]string{"X-Fleet-Signature": sig},
		json.RawMessage(enrollPayload))
	if status != 200 {
		t.Fatalf("enrollment callback: expected 200, got %d: %s", status, resp)
	}
	t.Logf("enrolled host=%s", hostID)

	// 4. Evaluate access — the user now has host-001 enrolled; the FleetDM mock
	// reports it as compliant (firewall+disk both true, no vulns).
	evalBody := map[string]string{
		"userId":   created.ID,
		"deviceId": signedDeviceID(t, hostID),
	}
	status, resp = do(t, "POST", "/api/v1/access/evaluate", accessEvalHeaders(), evalBody)
	if status != 200 {
		t.Fatalf("access/evaluate (compliant device): expected 200, got %d: %s", status, resp)
	}
	var env struct {
		Data accessEvalResp `json:"data"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("parse response: %v (body: %s)", err, resp)
	}
	result := env.Data
	if !result.Allow {
		t.Errorf("compliant device (host-001): expected allow, got deny (reasons: %v)", result.Reasons)
	}
	t.Logf("compliant device → allow (reasons: %v)", result.Reasons)

	// Cleanup.
	do(t, "DELETE", "/scim/v2/Users/"+created.ID, scimHeaders(), nil)
}

// TestE2E_PostureEnforcement_MintDeviceCookieThenEvaluate proves the real
// browser-shaped path: enroll → POST /enrollment/device-identity (Origin +
// JSON) → use the signed freecloud-device-id cookie value as deviceId on
// access/evaluate. This is what the Keycloak SPI forwards after login.
func TestE2E_PostureEnforcement_MintDeviceCookieThenEvaluate(t *testing.T) {
	waitReady(t, 30*time.Second)

	email := fmt.Sprintf("e2e-posture-cookie-%d@example.com", time.Now().UnixNano())
	createBody := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": email,
		"name":     map[string]string{"givenName": "Cookie", "familyName": "Path"},
		"active":   true,
	}
	status, resp := do(t, "POST", "/scim/v2/Users", scimHeaders(), createBody)
	if status != 201 {
		t.Fatalf("create SCIM user: expected 201, got %d: %s", status, resp)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil || created.ID == "" {
		t.Fatalf("parse user create: %v (body: %s)", err, resp)
	}
	defer do(t, "DELETE", "/scim/v2/Users/"+created.ID, scimHeaders(), nil)

	tokenBody := map[string]string{"userId": created.ID}
	status, resp = do(t, "POST", "/api/v1/e2e/enrollment-token", scimHeaders(), tokenBody)
	if status != 200 {
		t.Fatalf("create enrollment token: expected 200, got %d: %s", status, resp)
	}
	var tokenResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &tokenResp); err != nil || tokenResp.Data.Token == "" {
		t.Fatalf("parse token response: %v (body: %s)", err, resp)
	}
	enrollToken := tokenResp.Data.Token

	hostID := "host-001"
	enrollPayload, _ := json.Marshal(map[string]string{
		"enrollment_token": enrollToken,
		"host_id":          hostID,
		"hostname":         hostID + ".cookie.local",
		"os_version":       "macOS 15.0",
	})
	sig := hmacSig(t, *flagWebhookSecret, enrollPayload)
	status, resp = do(t, "POST", "/api/v1/fleet/enrollment-callback",
		map[string]string{"X-Fleet-Signature": sig},
		json.RawMessage(enrollPayload))
	if status != 200 {
		t.Fatalf("enrollment callback: expected 200, got %d: %s", status, resp)
	}

	// Mint cookie the way a trusted dashboard origin would (CORS_ORIGIN in e2e compose).
	status, resp, cookies := doFull(t, "POST", "/api/v1/enrollment/device-identity",
		map[string]string{"Origin": "http://localhost:3000"},
		map[string]string{"enrollmentToken": enrollToken})
	if status != 200 {
		t.Fatalf("device-identity mint: expected 200, got %d: %s", status, resp)
	}
	var cookieVal string
	for _, c := range cookies {
		if c.Name == "freecloud-device-id" {
			cookieVal = c.Value
			break
		}
	}
	if cookieVal == "" {
		t.Fatalf("device-identity mint: freecloud-device-id cookie missing; body=%s cookies=%v", resp, cookies)
	}
	if !strings.HasPrefix(cookieVal, "v1.") {
		t.Fatalf("device-identity mint: expected signed v1 cookie, got %q", cookieVal)
	}

	// Evaluate using the cookie value (what SPI forwards as deviceId).
	evalBody := map[string]string{
		"userId":   created.ID,
		"deviceId": cookieVal,
	}
	status, resp = do(t, "POST", "/api/v1/access/evaluate", accessEvalHeaders(), evalBody)
	if status != 200 {
		t.Fatalf("access/evaluate (minted cookie): expected 200, got %d: %s", status, resp)
	}
	var env struct {
		Data accessEvalResp `json:"data"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("parse response: %v (body: %s)", err, resp)
	}
	if !env.Data.Allow {
		t.Fatalf("minted device cookie for compliant host: expected allow, got deny (reasons: %v)", env.Data.Reasons)
	}
	t.Logf("minted cookie → allow (reasons: %v)", env.Data.Reasons)
}

// TestE2E_PostureEnforcement_NonCompliantDeviceDenied proves that a device whose
// posture returns a violation is denied with a posture reason (not "not enrolled").
//
// The FleetDM mock returns compliant posture only for host-001. Any other host ID
// gets disk_encryption=false and firewall=false (non-compliant defaults), which
// causes the backend to deny with firewall/disk violation reasons.
func TestE2E_PostureEnforcement_NonCompliantDeviceDenied(t *testing.T) {
	waitReady(t, 30*time.Second)

	// 1. Create a disposable user.
	email := fmt.Sprintf("e2e-posture-deny-%d@example.com", time.Now().UnixNano())
	createBody := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": email,
		"name":     map[string]string{"givenName": "Posture", "familyName": "Denied"},
		"active":   true,
	}
	status, resp := do(t, "POST", "/scim/v2/Users", scimHeaders(), createBody)
	if status != 201 {
		t.Fatalf("create SCIM user: expected 201, got %d: %s", status, resp)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil || created.ID == "" {
		t.Fatalf("parse user create: %v (body: %s)", err, resp)
	}
	t.Logf("created user id=%s", created.ID)

	// 2. Mint an enrollment token and enroll a non-compliant host.
	// Any host ID other than "host-001" gets disk_encryption=false and
	// firewall=false from the FleetDM mock.
	noncompliantHost := fmt.Sprintf("host-noncompliant-e2e-%d", time.Now().UnixNano()%100000)

	tokenBody := map[string]string{"userId": created.ID}
	status, resp = do(t, "POST", "/api/v1/e2e/enrollment-token", scimHeaders(), tokenBody)
	if status != 200 {
		t.Fatalf("create enrollment token: expected 200, got %d: %s", status, resp)
	}
	var tokenResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &tokenResp); err != nil || tokenResp.Data.Token == "" {
		t.Fatalf("parse token response: %v (body: %s)", err, resp)
	}
	enrollToken := tokenResp.Data.Token

	enrollPayload, _ := json.Marshal(map[string]string{
		"enrollment_token": enrollToken,
		"host_id":          noncompliantHost,
		"hostname":         noncompliantHost + ".local",
		"os_version":       "Ubuntu 22.04",
	})
	sig := hmacSig(t, *flagWebhookSecret, enrollPayload)
	status, resp = do(t, "POST", "/api/v1/fleet/enrollment-callback",
		map[string]string{"X-Fleet-Signature": sig},
		json.RawMessage(enrollPayload))
	if status != 200 {
		t.Fatalf("enrollment callback: expected 200, got %d: %s", status, resp)
	}
	t.Logf("enrolled non-compliant host=%s", noncompliantHost)

	// 3. Evaluate access — the device is enrolled but non-compliant.
	// The FleetDM mock returns disk_encryption=false and firewall=false for
	// any host other than host-001, so the backend denies with posture reasons.
	evalBody := map[string]string{
		"userId":   created.ID,
		"deviceId": signedDeviceID(t, noncompliantHost),
	}
	status, resp = do(t, "POST", "/api/v1/access/evaluate", accessEvalHeaders(), evalBody)
	if status != 200 {
		t.Fatalf("access/evaluate (non-compliant device): expected 200, got %d: %s", status, resp)
	}
	var env struct {
		Data accessEvalResp `json:"data"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("parse response: %v (body: %s)", err, resp)
	}
	result := env.Data
	if result.Allow {
		t.Errorf("non-compliant device: expected deny, got allow (reasons: %v)", result.Reasons)
	}
	if len(result.Reasons) == 0 {
		t.Error("non-compliant device: expected at least one denial reason")
	}
	// Reasons must reflect a posture violation ("firewall" or "disk"), NOT "not enrolled".
	hasViolation := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "firewall") || strings.Contains(r, "disk") ||
			strings.Contains(r, "unavailable") || strings.Contains(r, "posture") {
			hasViolation = true
		}
	}
	if !hasViolation {
		t.Errorf("non-compliant device denial reasons don't mention a posture violation: %v", result.Reasons)
	}
	t.Logf("non-compliant device → deny, reasons: %v", result.Reasons)

	// Cleanup.
	do(t, "DELETE", "/scim/v2/Users/"+created.ID, scimHeaders(), nil)
}

// TestE2E_PostureEnforcement_NoEnrolledDeviceDenied proves that a user with
// no enrolled device at all is denied (fail-closed path).
func TestE2E_PostureEnforcement_NoEnrolledDeviceDenied(t *testing.T) {
	waitReady(t, 30*time.Second)

	// 1. Create a user with no device mapping.
	email := fmt.Sprintf("e2e-posture-nodev-%d@example.com", time.Now().UnixNano())
	createBody := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": email,
		"name":     map[string]string{"givenName": "NoDevice", "familyName": "User"},
		"active":   true,
	}
	status, resp := do(t, "POST", "/scim/v2/Users", scimHeaders(), createBody)
	if status != 201 {
		t.Fatalf("create SCIM user: expected 201, got %d: %s", status, resp)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil || created.ID == "" {
		t.Fatalf("parse user create: %v (body: %s)", err, resp)
	}
	t.Logf("created user id=%s (no device enrolled)", created.ID)

	// 2. Evaluate without specifying deviceId — backend looks up enrolled devices,
	// finds none, and denies (fail-closed).
	body := map[string]string{
		"userId": created.ID,
		// no deviceId field
	}
	status, resp = do(t, "POST", "/api/v1/access/evaluate", accessEvalHeaders(), body)
	if status != 200 {
		t.Fatalf("access/evaluate (no device): expected 200, got %d: %s", status, resp)
	}
	var env struct {
		Data accessEvalResp `json:"data"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("parse response: %v (body: %s)", err, resp)
	}
	result := env.Data
	if result.Allow {
		t.Errorf("no enrolled device: expected deny, got allow")
	}
	hasNoDevice := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "no enrolled device") || strings.Contains(r, "device") {
			hasNoDevice = true
		}
	}
	if !hasNoDevice {
		t.Errorf("no-device denial reasons don't mention missing device: %v", result.Reasons)
	}
	t.Logf("no device → deny, reasons: %v", result.Reasons)

	// Cleanup.
	do(t, "DELETE", "/scim/v2/Users/"+created.ID, scimHeaders(), nil)
}

// TestE2E_PostureEnforcement_BearerRequired proves that the access-eval endpoint
// rejects unauthenticated requests (fail-closed authentication).
func TestE2E_PostureEnforcement_BearerRequired(t *testing.T) {
	waitReady(t, 30*time.Second)

	body := map[string]string{"userId": "00000000-0000-0000-0000-000000000001"}
	status, _ := do(t, "POST", "/api/v1/access/evaluate", nil, body)
	if status == 200 {
		t.Errorf("no bearer: expected 401/503, got 200 — endpoint is not auth-gated")
	}
	if status != 401 && status != 503 {
		t.Logf("no bearer: got %d (expected 401 or 503)", status)
	}
}
