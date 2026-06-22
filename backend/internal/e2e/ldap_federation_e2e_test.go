//go:build e2e

// Package e2e — LDAP/AD federation source CRUD tests (F2).
//
// TestE2E_LDAPFederation_SourceCRUD exercises Epic C's federation API:
//
//   - Create a federation source pointing at the openldap-e2e container.
//   - List federation sources and verify the new source appears.
//   - Attempt a connection test (success or failure accepted — we assert non-500).
//   - Delete the source and verify it is gone.
//
// The openldap-e2e compose service must be running and the backend must have
// LDAP_BIND_PASSWORD set (to "adminpassword") so CreateFederationSource can
// proceed past the bind-password guard.
package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ldapHeaders returns headers for JWT-gated admin endpoints.
// In APP_ENV=test the backend accepts the SCIM bearer token for all endpoints.
func ldapHeaders() map[string]string {
	return scimHeaders()
}

func TestE2E_LDAPFederation_SourceCRUD(t *testing.T) {
	waitReady(t, 60*time.Second)

	sourceName := fmt.Sprintf("e2e-ldap-%d", time.Now().UnixNano())

	// 1. Create a federation source pointing at the openldap-e2e container.
	// The compose service "openldap-e2e" listens on port 1389 (bitnami/openldap default).
	createBody := map[string]interface{}{
		"name":          sourceName,
		"vendor":        "other",
		"connectionUrl": "ldap://openldap-e2e:1389",
		"bindDn":        "cn=admin,dc=example,dc=com",
		"usersDn":       "ou=users,dc=example,dc=com",
	}
	status, body := do(t, "POST", "/api/v1/federation/sources", ldapHeaders(), createBody)
	if status != 201 && status != 200 {
		t.Fatalf("create federation source: expected 201, got %d: %s", status, body)
	}
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("parse create federation source: %v (body: %s)", err, body)
	}
	t.Logf("created federation source id=%s name=%s", created.ID, created.Name)

	// Cleanup deferred so the source is always removed even if asserts fail.
	t.Cleanup(func() {
		do(t, "DELETE", "/api/v1/federation/sources/"+created.ID, ldapHeaders(), nil)
		t.Logf("cleaned up federation source id=%s", created.ID)
	})

	// 2. List federation sources — verify the new source appears.
	status, body = do(t, "GET", "/api/v1/federation/sources", ldapHeaders(), nil)
	if status != 200 {
		t.Fatalf("list federation sources: expected 200, got %d: %s", status, body)
	}
	if !strings.Contains(string(body), created.ID) {
		t.Errorf("list federation sources: created source id %s not found in response: %s", created.ID, body)
	}
	t.Logf("federation source appears in list")

	// 3. GET the source by ID.
	status, body = do(t, "GET", "/api/v1/federation/sources/"+created.ID, ldapHeaders(), nil)
	if status != 200 {
		t.Fatalf("get federation source: expected 200, got %d: %s", status, body)
	}
	assertContains(t, body, sourceName)

	// 4. Connection test — accepts success OR failure; must not be a 5xx.
	// The openldap-e2e container may or may not have the exact usersDn populated
	// during the test; we only assert the endpoint is wired and not broken.
	status, body = do(t, "POST", "/api/v1/federation/sources/"+created.ID+"/test", ldapHeaders(), nil)
	if status >= 500 {
		t.Errorf("test federation connection: unexpected 5xx response %d: %s", status, body)
	}
	t.Logf("connection test → %d: %s", status, body)

	// 5. Delete the source (also exercised by t.Cleanup, but assert 200 here).
	status, body = do(t, "DELETE", "/api/v1/federation/sources/"+created.ID, ldapHeaders(), nil)
	if status != 200 {
		t.Fatalf("delete federation source: expected 200, got %d: %s", status, body)
	}
	t.Logf("deleted federation source id=%s", created.ID)

	// 6. Verify it is gone — GET should return 404.
	status, _ = do(t, "GET", "/api/v1/federation/sources/"+created.ID, ldapHeaders(), nil)
	if status != 404 {
		t.Errorf("get deleted source: expected 404, got %d", status)
	}
}
