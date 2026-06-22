//go:build e2e

// Package e2e — outbound SCIM provisioning round-trip tests (F2).
//
// TestE2E_SCIMOut_ProvisioningConfigRoundTrip wires FreeCloud's outbound SCIM
// connector to point at its own inbound /scim/v2 endpoint (self-loop), verifying
// that provisioning config persists, the GET round-trips correctly, a SCIM user
// can be created, a manual resync is accepted, and the provisioning state endpoint
// is reachable.  The actual async sync outcome is not asserted — we verify the
// plumbing, not the eventual consistency.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestE2E_SCIMOut_ProvisioningConfigRoundTrip(t *testing.T) {
	waitReady(t, 60*time.Second)

	// 1. Create an app to attach provisioning config to.
	appName := fmt.Sprintf("e2e-scim-out-app-%d", time.Now().UnixNano())
	createAppBody := map[string]interface{}{
		"name":         appName,
		"protocol":     "OIDC",
		"redirectURIs": []string{"http://localhost:3000/callback"},
		"baseURL":      "http://localhost:3000",
	}
	status, body := do(t, http.MethodPost, "/api/v1/apps/create", scimHeaders(), createAppBody)
	if status != 200 && status != 201 {
		t.Fatalf("create app: expected 200/201, got %d: %s", status, body)
	}
	var appResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		// Some handlers return the object at the top level.
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &appResp); err != nil {
		t.Fatalf("parse create app response: %v (body: %s)", err, body)
	}
	appID := appResp.Data.ID
	if appID == "" {
		appID = appResp.ID
	}
	if appID == "" {
		t.Fatalf("create app: could not extract app ID from response: %s", body)
	}
	t.Logf("created app id=%s", appID)

	// 2. Configure outbound SCIM provisioning — point at the backend itself
	// (inside the container, the backend reaches its own inbound SCIM at
	// http://localhost:8080/scim/v2).
	provConfig := map[string]interface{}{
		"enabled":       true,
		"connectorType": "scim",
		"endpointUrl":   "http://localhost:8080/scim/v2",
		"bearerToken":   "e2e-scim-token",
		"attributeMap":  map[string]interface{}{},
	}
	status, body = do(t, http.MethodPut, "/api/v1/apps/"+appID+"/provisioning", scimHeaders(), provConfig)
	if status != 200 && status != 201 && status != 204 {
		t.Fatalf("configure provisioning: expected 200/201/204, got %d: %s", status, body)
	}
	t.Logf("provisioning configured (status %d)", status)

	// 3. GET provisioning config and verify it round-trips.
	status, body = do(t, http.MethodGet, "/api/v1/apps/"+appID+"/provisioning", scimHeaders(), nil)
	if status != 200 {
		t.Fatalf("get provisioning config: expected 200, got %d: %s", status, body)
	}
	var getResp struct {
		Data struct {
			Enabled              bool   `json:"enabled"`
			ConnectorType        string `json:"connectorType"`
			BearerTokenConfigured bool   `json:"bearerTokenConfigured"`
		} `json:"data"`
		// Also accept flat response.
		Enabled              bool   `json:"enabled"`
		ConnectorType        string `json:"connectorType"`
		BearerTokenConfigured bool   `json:"bearerTokenConfigured"`
	}
	if err := json.Unmarshal(body, &getResp); err != nil {
		t.Fatalf("parse get provisioning response: %v (body: %s)", err, body)
	}
	enabled := getResp.Data.Enabled || getResp.Enabled
	if !enabled {
		t.Errorf("provisioning config GET: expected enabled=true, got false (body: %s)", body)
	}
	t.Logf("provisioning config GET: enabled=%v, body=%s", enabled, body)

	// 4. Create a SCIM user via the inbound endpoint — after this the user exists
	// in the local DB and can be referenced for a resync.
	userName := fmt.Sprintf("e2e-scim-out-%d@example.com", time.Now().UnixNano())
	createUserBody := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": userName,
		"name":     map[string]string{"givenName": "SCIM", "familyName": "OutTest"},
		"active":   true,
	}
	status, body = do(t, http.MethodPost, "/scim/v2/Users", scimHeaders(), createUserBody)
	if status != 201 {
		t.Fatalf("create SCIM user: expected 201, got %d: %s", status, body)
	}
	var userCreated struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &userCreated); err != nil || userCreated.ID == "" {
		t.Fatalf("parse user create response: %v (body: %s)", err, body)
	}
	userID := userCreated.ID
	t.Logf("created SCIM user id=%s", userID)

	// 5. Trigger a manual resync for this user.
	status, body = do(t, http.MethodPost, "/api/v1/apps/"+appID+"/provisioning/resync/"+userID, scimHeaders(), nil)
	if status >= 500 {
		t.Errorf("resync user: unexpected 5xx response %d: %s", status, body)
	}
	t.Logf("resync user %s → %d", userID, status)

	// 6. Verify the provisioning state endpoint is reachable (async — no outcome assert).
	status, body = do(t, http.MethodGet, "/api/v1/apps/"+appID+"/provisioning/state", scimHeaders(), nil)
	if status >= 500 {
		t.Errorf("provisioning state: unexpected 5xx response %d: %s", status, body)
	}
	t.Logf("provisioning state → %d", status)

	// Cleanup.
	do(t, http.MethodDelete, "/scim/v2/Users/"+userID, scimHeaders(), nil)
}
