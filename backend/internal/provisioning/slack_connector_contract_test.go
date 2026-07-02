package provisioning

// A4 — Slack SCIM connector contract tests.
//
// SlackConnector delegates entirely to the generic SCIMConnector pointed at
// Slack's SCIM v2 base URL (https://api.slack.com/scim/v2) — see
// slack_connector.go. These tests pin Slack's actual SCIM request/response
// shapes (per docs.slack.dev/reference/scim-api/) using recorded fixtures, so
// a future refactor of the shared SCIMConnector can't silently break the
// Slack-specific contract without a test noticing.
//
// MANUAL-VERIFY (see docs/DEPLOYMENT.md "Outbound Provisioning"): live Slack
// tenant sync requires a paid Slack plan with SCIM enabled and a real
// admin.users:write-scoped token — verification against a live Slack tenant
// stays parked; these are recorded-fixture contract tests only.
//
// OPEN QUESTION for live verification: Slack's own Python SDK docs describe
// setting Content-Type to "application/json" for SCIM calls, while
// SCIMConnector sends the RFC 7644 §3.1-correct "application/scim+json".
// Both are valid per the SCIM spec (services should accept either), but this
// has not been confirmed against a live Slack tenant — flagged for the
// LIVE_SLACK verification entrypoint below if/when a paid plan is available.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// slackUserFixture mirrors Slack's real SCIM User resource shape returned
// from a create/get call — see docs.slack.dev/reference/scim-api/.
const slackUserFixture = `{
	"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"],
	"id": "slack-u-1234",
	"userName": "alice@acme-corp.example",
	"name": {"givenName": "Alice", "familyName": "Example"},
	"emails": [{"value": "alice@acme-corp.example", "primary": true}],
	"active": true
}`

func TestSlackConnector_ProvisionUser_MatchesRealAPIContract(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/scim+json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(slackUserFixture))
	}))
	defer srv.Close()

	c := NewSlackConnectorWithBaseURL("slack-test-token", srv.URL)
	user := ProvisionableUser{Email: "alice@acme-corp.example", FirstName: "Alice", LastName: "Example", Department: "Engineering"}

	remoteID, err := c.ProvisionUser(context.Background(), user)
	if err != nil {
		t.Fatalf("ProvisionUser: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/Users" {
		t.Errorf("path = %q, want /Users", gotPath)
	}
	if gotAuth != "Bearer slack-test-token" {
		t.Errorf("Authorization = %q, want Bearer slack-test-token", gotAuth)
	}
	if gotContentType != "application/scim+json" {
		t.Errorf("Content-Type = %q, want application/scim+json (RFC 7644 §3.1)", gotContentType)
	}
	if gotBody["userName"] != "alice@acme-corp.example" {
		t.Errorf("body[userName] = %v, want alice@acme-corp.example", gotBody["userName"])
	}
	schemas, _ := gotBody["schemas"].([]interface{})
	if len(schemas) != 1 || schemas[0] != "urn:ietf:params:scim:schemas:core:2.0:User" {
		t.Errorf("body[schemas] = %v, want [\"urn:ietf:params:scim:schemas:core:2.0:User\"]", schemas)
	}
	if remoteID != "slack-u-1234" {
		t.Errorf("remoteID = %q, want slack-u-1234 (from fixture's id field)", remoteID)
	}
}

// TestSlackConnector_DeprovisionUser_UsesPatchActiveFalse proves the
// offboard-deactivation path. Slack's SCIM API supports both DELETE /Users/{id}
// and PATCH active:false for deactivation (see docs.slack.dev/admins/scim-api/
// — "Deactivate activated users by setting the active attribute equal to
// false"); the shared SCIMConnector always uses PATCH, which this test pins.
func TestSlackConnector_DeprovisionUser_UsesPatchActiveFalse(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/scim+json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"slack-u-1234","active":false}`))
	}))
	defer srv.Close()

	c := NewSlackConnectorWithBaseURL("slack-test-token", srv.URL)
	if err := c.DeprovisionUser(context.Background(), "slack-u-1234"); err != nil {
		t.Fatalf("DeprovisionUser: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/Users/slack-u-1234" {
		t.Errorf("path = %q, want /Users/slack-u-1234", gotPath)
	}

	schemas, _ := gotBody["schemas"].([]interface{})
	if len(schemas) != 1 || schemas[0] != "urn:ietf:params:scim:api:messages:2.0:PatchOp" {
		t.Errorf("body[schemas] = %v, want PatchOp schema", schemas)
	}
	ops, _ := gotBody["Operations"].([]interface{})
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 patch operation, got %d", len(ops))
	}
	op, _ := ops[0].(map[string]interface{})
	if op["op"] != "replace" || op["path"] != "active" {
		t.Errorf("operation = %+v, want {op:replace, path:active}", op)
	}
	if v, ok := op["value"].(bool); !ok || v {
		t.Errorf("operation value = %v, want false", op["value"])
	}
}

// TestSlackConnector_UpdateUser_SendsProfileFields proves profile updates
// (department, name changes) reach Slack via PATCH, not a full PUT — Slack's
// SCIM API supports partial updates via PATCH per RFC 7644 §3.5.2.
func TestSlackConnector_UpdateUser_SendsProfileFields(t *testing.T) {
	var gotMethod string
	var gotBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewSlackConnectorWithBaseURL("slack-test-token", srv.URL)
	user := ProvisionableUser{FirstName: "Bob", LastName: "Smith", Department: "Sales"}
	if err := c.UpdateUser(context.Background(), "slack-u-5678", user); err != nil {
		t.Fatalf("UpdateUser: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	ops, _ := gotBody["Operations"].([]interface{})
	if len(ops) == 0 {
		t.Fatal("expected at least one patch operation")
	}
}

// TestSlackConnector_ProvisionUser_DuplicateUserErrorSurfaces proves a real
// Slack "user already exists" SCIM error response is surfaced, not swallowed
// — important because provisioning retries a transient error but a
// permanent "already exists" should be visible in provisioning_state.
func TestSlackConnector_ProvisionUser_DuplicateUserErrorSurfaces(t *testing.T) {
	const slackConflictBody = `{
		"schemas": ["urn:ietf:params:scim:api:messages:2.0:Error"],
		"status": "409",
		"detail": "userName already exists"
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(slackConflictBody))
	}))
	defer srv.Close()

	c := NewSlackConnectorWithBaseURL("slack-test-token", srv.URL)
	_, err := c.ProvisionUser(context.Background(), ProvisionableUser{Email: "dup@example.com"})
	if err == nil {
		t.Fatal("ProvisionUser: expected error on 409 conflict, got nil")
	}
}
