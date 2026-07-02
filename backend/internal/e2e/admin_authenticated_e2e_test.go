//go:build e2e

// Package e2e — authenticated admin round-trip tests (A1).
//
// Prior to A1 the harness had no admin-JWT path, so JWT-gated admin routes
// were only smoke-tested (401-gated) — see ldap_federation_e2e_test.go and
// scim_out_e2e_test.go. adminHeaders() (admin_auth.go) now mints a real admin
// JWT from Keycloak via the e2e-only bootstrap seed (E2E_SEED_ADMIN=true),
// so this file upgrades those smokes into fully authenticated round-trips:
// provisioning-config CRUD, federation CRUD, app catalog, and one end-to-end
// provisioning flow (create employee via SCIM → connector provisions via a
// self-loop back into this backend's own inbound SCIM endpoint → verify
// provisioning_state transitions to "provisioned").
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ---- Provisioning config CRUD (authenticated) ----

func TestE2E_Admin_ProvisioningConfig_CRUD(t *testing.T) {
	waitReady(t, 60*time.Second)
	admin := adminHeaders(t)

	appID := createOIDCApp(t, admin, fmt.Sprintf("e2e-prov-app-%d", time.Now().UnixNano()))

	// GET before any config exists — zero-value response, not 404/500.
	status, body := do(t, http.MethodGet, "/api/v1/apps/"+appID+"/provisioning", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("get provisioning config (none yet): expected 200, got %d: %s", status, body)
	}

	// PUT — configure a SCIM connector pointed at this backend's own inbound
	// SCIM endpoint (self-loop) so the round-trip test below has a real,
	// in-stack target without needing an extra mock container.
	putBody := map[string]interface{}{
		"enabled":       true,
		"connectorType": "scim",
		"endpointUrl":   "http://backend-e2e:8080/scim/v2",
		"bearerToken":   *flagSCIMToken,
	}
	status, body = do(t, http.MethodPut, "/api/v1/apps/"+appID+"/provisioning", admin, putBody)
	if status != http.StatusOK {
		t.Fatalf("put provisioning config: expected 200, got %d: %s", status, body)
	}
	var putResp struct {
		Data struct {
			Enabled               bool   `json:"enabled"`
			ConnectorType         string `json:"connectorType"`
			BearerTokenConfigured bool   `json:"bearerTokenConfigured"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &putResp); err != nil {
		t.Fatalf("parse put response: %v (body: %s)", err, body)
	}
	if !putResp.Data.Enabled || putResp.Data.ConnectorType != "scim" || !putResp.Data.BearerTokenConfigured {
		t.Errorf("put provisioning config: unexpected response: %+v", putResp.Data)
	}

	// GET after PUT — reflects the saved config; bearer token is never echoed
	// back in plaintext, only the "configured" flag.
	status, body = do(t, http.MethodGet, "/api/v1/apps/"+appID+"/provisioning", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("get provisioning config (after put): expected 200, got %d: %s", status, body)
	}
	var getResp struct {
		Data struct {
			ConnectorType         string `json:"connectorType"`
			EndpointURL           string `json:"endpointUrl"`
			BearerTokenConfigured bool   `json:"bearerTokenConfigured"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &getResp); err != nil {
		t.Fatalf("parse get response: %v (body: %s)", err, body)
	}
	if getResp.Data.EndpointURL != "http://backend-e2e:8080/scim/v2" || !getResp.Data.BearerTokenConfigured {
		t.Errorf("get provisioning config: unexpected persisted config: %+v", getResp.Data)
	}

	// List provisioning state — empty but 200, not error.
	status, body = do(t, http.MethodGet, "/api/v1/apps/"+appID+"/provisioning/state", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list provisioning state: expected 200, got %d: %s", status, body)
	}
}

// ---- Federation source CRUD (authenticated, against the real openldap-e2e container) ----

func TestE2E_Admin_FederationSource_CRUD(t *testing.T) {
	waitReady(t, 60*time.Second)
	admin := adminHeaders(t)

	name := fmt.Sprintf("e2e-ldap-%d", time.Now().UnixNano())
	createBody := map[string]interface{}{
		"name":          name,
		"vendor":        "other",
		"connectionUrl": "ldap://openldap-e2e:1389",
		"bindDn":        "cn=admin,dc=example,dc=com",
		"usersDn":       "dc=example,dc=com",
	}
	status, body := do(t, http.MethodPost, "/api/v1/federation/sources", admin, createBody)
	if status != http.StatusCreated {
		t.Fatalf("create federation source: expected 201, got %d: %s", status, body)
	}
	var created struct {
		Data struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Vendor string `json:"vendor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("parse create federation response: %v (body: %s)", err, body)
	}
	fsID := created.Data.ID
	t.Logf("created federation source id=%s", fsID)

	// List — the new source appears.
	status, body = do(t, http.MethodGet, "/api/v1/federation/sources", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list federation sources: expected 200, got %d: %s", status, body)
	}
	if !jsonContainsID(body, fsID) {
		t.Errorf("list federation sources: created source %s not found in list: %s", fsID, body)
	}

	// Get single.
	status, body = do(t, http.MethodGet, "/api/v1/federation/sources/"+fsID, admin, nil)
	if status != http.StatusOK {
		t.Fatalf("get federation source: expected 200, got %d: %s", status, body)
	}

	// Test connection — proves the backend can actually bind to the live
	// openldap-e2e container with the configured credentials.
	status, body = do(t, http.MethodPost, "/api/v1/federation/sources/"+fsID+"/test", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("test federation connection: expected 200, got %d: %s", status, body)
	}
	var testResult struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &testResult); err == nil && !testResult.Data.Success {
		t.Errorf("test federation connection: expected success against live openldap-e2e, got failure: %s", body)
	}

	// Trigger a live sync — this calls Keycloak's real user-storage sync
	// endpoint against the openldap-e2e container, which is seeded with two
	// users (docker/openldap-e2e/seed.ldif). This proves the live-LDAP-sync
	// path end-to-end at the Keycloak layer. It does NOT assert the synced
	// users appear in FreeCloud's local `users` table — that requires the
	// separate reconcile job (RECONCILE_INTERVAL, default 15m), which is too
	// slow to wait on here; DB-visibility of federated users is exercised by
	// the reconcile package's own unit tests.
	status, body = do(t, http.MethodPost, "/api/v1/federation/sources/"+fsID+"/sync", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("trigger federation sync: expected 200, got %d: %s", status, body)
	}
	var syncResult struct {
		Data struct {
			Synced bool `json:"synced"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &syncResult); err == nil && !syncResult.Data.Synced {
		t.Errorf("trigger federation sync: expected synced=true against live openldap-e2e, got: %s", body)
	}

	// Update.
	newName := name + "-renamed"
	status, body = do(t, http.MethodPatch, "/api/v1/federation/sources/"+fsID,
		admin, map[string]interface{}{"name": newName})
	if status != http.StatusOK {
		t.Fatalf("update federation source: expected 200, got %d: %s", status, body)
	}

	// Delete.
	status, body = do(t, http.MethodDelete, "/api/v1/federation/sources/"+fsID, admin, nil)
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("delete federation source: expected 200/204, got %d: %s", status, body)
	}
}

// ---- App catalog (authenticated) ----

func TestE2E_Admin_AppCatalog_CreateFromTemplate(t *testing.T) {
	waitReady(t, 60*time.Second)
	admin := adminHeaders(t)

	status, body := do(t, http.MethodGet, "/api/v1/apps/templates", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list app templates: expected 200, got %d: %s", status, body)
	}
	if !jsonContainsID(body, "generic-oidc") {
		t.Fatalf("app templates: expected to find generic-oidc template: %s", body)
	}

	name := fmt.Sprintf("e2e-catalog-app-%d", time.Now().UnixNano())
	createBody := map[string]interface{}{
		"name": name,
		"fields": map[string]string{
			"baseURL":     "https://e2e-catalog-app.example.com",
			"redirectURI": "https://e2e-catalog-app.example.com/auth/callback",
		},
	}
	status, body = do(t, http.MethodPost, "/api/v1/apps/templates/generic-oidc/create", admin, createBody)
	if status != http.StatusOK {
		t.Fatalf("create app from template: expected 200, got %d: %s", status, body)
	}
	var created struct {
		Data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("parse create-from-template response: %v (body: %s)", err, body)
	}
	t.Logf("created app from template id=%s", created.Data.ID)

	// Confirm it shows up in the main apps list.
	status, body = do(t, http.MethodGet, "/api/v1/apps", admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list apps: expected 200, got %d: %s", status, body)
	}
	if !jsonContainsID(body, created.Data.ID) {
		t.Errorf("list apps: created app %s not found: %s", created.Data.ID, body)
	}
}

// ---- Full provisioning round-trip: create employee -> connector provisions -> verify ----

func TestE2E_Admin_ProvisioningRoundTrip(t *testing.T) {
	waitReady(t, 60*time.Second)
	admin := adminHeaders(t)

	// 1. Create the target app and point its provisioning config at the
	// dedicated scim-mock-e2e container (a genuinely separate downstream
	// target). Self-looping outbound provisioning back into this backend's
	// own inbound SCIM doesn't work: the employee created in step 2 below
	// already exists there by email, so the connector's create call 409s.
	//
	// endpointUrl is the backend CONTAINER's view of the mock (the compose
	// internal hostname — the backend, not this test process, makes the
	// outbound call), which is why it's a fixed internal URL rather than
	// flagSCIMMockURL (that flag is the host-facing port mapping, unused
	// here since this test never calls the mock directly).
	appID := createOIDCApp(t, admin, fmt.Sprintf("e2e-roundtrip-app-%d", time.Now().UnixNano()))
	putBody := map[string]interface{}{
		"enabled":       true,
		"connectorType": "scim",
		"endpointUrl":   "http://scim-mock-e2e:8080",
		"bearerToken":   *flagSCIMMockToken,
	}
	status, body := do(t, http.MethodPut, "/api/v1/apps/"+appID+"/provisioning", admin, putBody)
	if status != http.StatusOK {
		t.Fatalf("configure provisioning: expected 200, got %d: %s", status, body)
	}

	// 2. Create the employee via SCIM (mirrors an IdP pushing a new hire).
	email := fmt.Sprintf("e2e-roundtrip-%d@example.com", time.Now().UnixNano())
	createUserBody := map[string]interface{}{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": email,
		"name":     map[string]string{"givenName": "RoundTrip", "familyName": "Employee"},
		"active":   true,
	}
	status, body = do(t, http.MethodPost, "/scim/v2/Users", scimHeaders(), createUserBody)
	if status != http.StatusCreated {
		t.Fatalf("create employee via SCIM: expected 201, got %d: %s", status, body)
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &user); err != nil || user.ID == "" {
		t.Fatalf("parse SCIM create-user response: %v (body: %s)", err, body)
	}
	t.Logf("created employee user_id=%s", user.ID)
	defer do(t, http.MethodDelete, "/scim/v2/Users/"+user.ID, scimHeaders(), nil)

	// 3. Trigger provisioning explicitly (mirrors app-assignment-driven resync).
	// This is async (202 Accepted) — the connector call happens in a goroutine.
	status, body = do(t, http.MethodPost,
		"/api/v1/apps/"+appID+"/provisioning/resync/"+user.ID, admin, nil)
	if status != http.StatusAccepted {
		t.Fatalf("resync user: expected 202, got %d: %s", status, body)
	}

	// 4. Poll provisioning_state until it shows "provisioned" — the connector
	// pushed a real SCIM create against this backend's own /scim/v2/Users.
	deadline := time.Now().Add(20 * time.Second)
	var lastState string
	for time.Now().Before(deadline) {
		status, body = do(t, http.MethodGet, "/api/v1/apps/"+appID+"/provisioning/state", admin, nil)
		if status != http.StatusOK {
			t.Fatalf("list provisioning state: expected 200, got %d: %s", status, body)
		}
		var stateResp struct {
			Data []struct {
				UserID   string `json:"userId"`
				Status   string `json:"status"`
				RemoteID string `json:"remoteId"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &stateResp); err != nil {
			t.Fatalf("parse provisioning state: %v (body: %s)", err, body)
		}
		for _, s := range stateResp.Data {
			if s.UserID == user.ID {
				lastState = s.Status
				if s.Status == "provisioned" {
					if s.RemoteID == "" {
						t.Errorf("provisioning state: status=provisioned but remoteId is empty")
					}
					t.Logf("provisioning round-trip complete: user=%s remote_id=%s", user.ID, s.RemoteID)
					return
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("provisioning round-trip: user %s never reached status=provisioned (last seen: %q)", user.ID, lastState)
}

// ---- helpers ----

// createOIDCApp creates a real OIDC connected app via the admin API and
// returns its FreeCloud app UUID (connected_apps.id).
func createOIDCApp(t *testing.T, admin map[string]string, name string) string {
	t.Helper()
	createBody := map[string]interface{}{
		"name":         name,
		"protocol":     "OIDC",
		"redirectURIs": []string{"https://" + name + ".example.com/callback"},
		"baseURL":      "https://" + name + ".example.com",
	}
	status, body := do(t, http.MethodPost, "/api/v1/apps/create", admin, createBody)
	if status != http.StatusOK {
		t.Fatalf("create app %q: expected 200, got %d: %s", name, status, body)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("parse create-app response: %v (body: %s)", err, body)
	}
	return created.Data.ID
}

// jsonContainsID is a loose helper: reports whether the given id/identifier
// substring appears anywhere in the raw JSON body. Used for list-endpoint
// membership checks where unmarshalling the full envelope isn't worth the
// verbosity.
func jsonContainsID(body []byte, id string) bool {
	return strings.Contains(string(body), id)
}
