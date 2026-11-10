package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeKC is a minimal httptest.Server simulating the Keycloak admin REST API for
// bootstrap idempotency tests. It tracks which resources have been "created".
type fakeKC struct {
	srv               *httptest.Server
	realmExists       bool
	groups            map[string]bool
	clientExists      bool
	clientSecret      string
	realmCreateCalls  int32
	groupCreateCalls  int32
	clientCreateCalls int32
	// clientSecretRegenCalls counts RegenerateClientSecret calls. The
	// service-account secret must stay stable across boots (issue #166).
	clientSecretRegenCalls int32

	// H8: access-token-lifespan defense-in-depth.
	// realmPutCalls counts EVERY PUT /admin/realms/freecloud call —
	// ensurePostureFlow (below) ALSO PUTs the realm on every run (to bind
	// browserFlow), independent of this fix, so tests use this to confirm
	// the lifespan check doesn't add an EXTRA call when nothing needs to
	// change. realmUpdateCallsWithLifespan additionally narrows to calls
	// whose body sets accessTokenLifespan, to confirm the value is correct
	// when a call is expected.
	realmPutCalls                int32
	realmUpdateCallsWithLifespan int32
	realmAccessTokenLifespan     *int // pre-set to simulate an existing realm's current value; nil = field absent
	createdAccessTokenLifespan   *int
	updatedAccessTokenLifespan   *int

	// Realm hardening: realmFields are extra fields served on GET realm
	// (simulating a pre-existing realm's current values); the created/updated
	// bodies capture what bootstrap sent so tests can assert every field.
	realmFields        map[string]interface{}
	createdRealmBody   map[string]interface{}
	updatedRealmBodies []map[string]interface{}

	// A1: e2e-admin seeding.
	e2eAdminExists           bool
	e2eAdminUserCreateCalls  int32
	e2eAdminPasswordSetCalls int32
	e2eAdminHasRole          bool
	dashboardDirectGrants    bool
	dashboardUpdateCalls     int32
	// Dashboard redirect URIs: bootstrap must stamp the Auth.js callback
	// or Keycloak rejects every authorize request (empty redirectUris).
	dashboardAbsent              bool
	dashboardRedirectURIs        []string
	dashboardWebOrigins          []string
	dashboardCreateCalls         int32
	dashboardRedirectUpdateCalls int32

	// A3: posture-flow execution ordering.
	lowerPriorityCalls int32

	// KC 26.8.2 kc-admin-api client scope.
	kcAdminAPIScopeExists     bool
	kcAdminAPIDefaultAssigned bool
	addDefaultScopeCalls      int32
}

// stringSliceBody extracts a JSON string array field from a decoded body.
func stringSliceBody(body map[string]interface{}, key string) []string {
	raw, ok := body[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func newFakeKC(t *testing.T) *fakeKC {
	t.Helper()
	f := &fakeKC{groups: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

// handle is a catch-all handler that routes by path and method.
// Using a single handler avoids Go ServeMux trailing-slash redirect issues.
func (f *fakeKC) handle(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	m := r.Method

	// Master admin token
	if strings.HasSuffix(p, "/protocol/openid-connect/token") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":300,"token_type":"Bearer"}`))
		return
	}

	// POST /admin/realms (create realm) — path ends with /realms or /realms/
	if m == http.MethodPost && (p == "/admin/realms" || p == "/admin/realms/") {
		atomic.AddInt32(&f.realmCreateCalls, 1)
		f.realmExists = true
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.createdRealmBody = body
		if v, ok := body["accessTokenLifespan"].(float64); ok {
			iv := int(v)
			f.createdAccessTokenLifespan = &iv
		}
		w.Header().Set("Location", f.srv.URL+"/admin/realms/freecloud")
		w.WriteHeader(http.StatusCreated)
		return
	}

	// GET/PUT /admin/realms/freecloud (exact realm)
	if p == "/admin/realms/freecloud" {
		if m == http.MethodGet {
			if f.realmExists {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]interface{}{"realm": "freecloud", "enabled": true, "browserFlow": "browser"}
				if f.realmAccessTokenLifespan != nil {
					body["accessTokenLifespan"] = *f.realmAccessTokenLifespan
				}
				for k, v := range f.realmFields {
					body[k] = v
				}
				_ = json.NewEncoder(w).Encode(body)
			} else {
				http.Error(w, `{"error":"Realm not found."}`, http.StatusNotFound)
			}
			return
		}
		if m == http.MethodPut {
			atomic.AddInt32(&f.realmPutCalls, 1)
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.updatedRealmBodies = append(f.updatedRealmBodies, body)
			if v, ok := body["accessTokenLifespan"].(float64); ok {
				iv := int(v)
				f.updatedAccessTokenLifespan = &iv
				atomic.AddInt32(&f.realmUpdateCallsWithLifespan, 1)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// GET /admin/realms/freecloud/roles/admin — admin realm role exists.
	if strings.HasSuffix(p, "/roles/admin") && m == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "role-admin", "name": "admin"})
		return
	}

	// GET/POST /admin/realms/freecloud/groups
	if p == "/admin/realms/freecloud/groups" || p == "/admin/realms/freecloud/groups/" {
		if m == http.MethodGet {
			search := r.URL.Query().Get("search")
			w.Header().Set("Content-Type", "application/json")
			if search != "" && f.groups[search] {
				_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "grp-1", "name": search}})
			} else {
				_ = json.NewEncoder(w).Encode([]map[string]string{})
			}
			return
		}
		if m == http.MethodPost {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.groups[body["name"]] = true
			atomic.AddInt32(&f.groupCreateCalls, 1)
			w.Header().Set("Location", f.srv.URL+"/admin/realms/freecloud/groups/grp-new")
			w.WriteHeader(http.StatusCreated)
			return
		}
	}

	// GET /admin/realms/freecloud/client-scopes
	if p == "/admin/realms/freecloud/client-scopes" && m == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		if f.kcAdminAPIScopeExists {
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "kc-admin-api-scope-uuid", "name": "kc-admin-api", "protocol": "openid-connect"}})
		} else {
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
		}
		return
	}

	// GET /admin/realms/freecloud/clients/svc-uuid/default-client-scopes
	if p == "/admin/realms/freecloud/clients/svc-uuid/default-client-scopes" && m == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		if f.kcAdminAPIDefaultAssigned {
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "kc-admin-api-scope-uuid", "name": "kc-admin-api", "protocol": "openid-connect"}})
		} else {
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
		}
		return
	}

	// PUT /admin/realms/freecloud/clients/svc-uuid/default-client-scopes/{scopeID}
	if strings.HasPrefix(p, "/admin/realms/freecloud/clients/svc-uuid/default-client-scopes/") && m == http.MethodPut {
		atomic.AddInt32(&f.addDefaultScopeCalls, 1)
		if strings.HasSuffix(p, "/kc-admin-api-scope-uuid") {
			// Real KC answers this PUT idempotently; a 409 here also
			// exercises the already-assigned tolerance in bootstrap.
			if f.kcAdminAPIDefaultAssigned {
				http.Error(w, `{"errorMessage":"Client scope is already assigned"}`, http.StatusConflict)
				return
			}
			f.kcAdminAPIDefaultAssigned = true
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// GET /admin/realms/freecloud/clients/svc-uuid/client-secret
	// (Keycloak's admin endpoint that returns the client's current secret).
	if p == "/admin/realms/freecloud/clients/svc-uuid/client-secret" && m == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"type": "secret", "value": f.clientSecret})
		return
	}

	// GET/POST /admin/realms/freecloud/clients
	if p == "/admin/realms/freecloud/clients" || p == "/admin/realms/freecloud/clients/" {
		if m == http.MethodGet {
			clientID := r.URL.Query().Get("clientId")
			w.Header().Set("Content-Type", "application/json")
			switch clientID {
			case "freecloud-service":
				if f.clientExists {
					secret := f.clientSecret
					if secret == "" {
						secret = "old-secret"
					}
					_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "svc-uuid", "clientId": "freecloud-service", "secret": secret}})
				} else {
					_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
				}
			case "freecloud-dashboard":
				if f.dashboardAbsent {
					_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
				} else {
					_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
						"id": "dash-uuid", "clientId": "freecloud-dashboard",
						"directAccessGrantsEnabled": f.dashboardDirectGrants,
						"redirectUris":              f.dashboardRedirectURIs,
						"webOrigins":                f.dashboardWebOrigins,
					}})
				}
			case "realm-management":
				_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "rm-uuid", "clientId": "realm-management"}})
			default:
				_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
			}
			return
		}
		if m == http.MethodPost {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if cid, ok := body["clientId"].(string); ok && cid == "freecloud-service" {
				f.clientExists = true
				if s, ok := body["secret"].(string); ok {
					f.clientSecret = s
				}
				atomic.AddInt32(&f.clientCreateCalls, 1)
			}
			if cid, ok := body["clientId"].(string); ok && cid == "freecloud-dashboard" {
				f.dashboardAbsent = false
				f.dashboardRedirectURIs = stringSliceBody(body, "redirectUris")
				f.dashboardWebOrigins = stringSliceBody(body, "webOrigins")
				atomic.AddInt32(&f.dashboardCreateCalls, 1)
			}
			w.Header().Set("Location", f.srv.URL+"/admin/realms/freecloud/clients/svc-uuid")
			w.WriteHeader(http.StatusCreated)
			return
		}
	}

	// PUT /admin/realms/freecloud/clients/svc-uuid (UpdateClient for override secret)
	if p == "/admin/realms/freecloud/clients/svc-uuid" && m == http.MethodPut {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if s, ok := body["secret"].(string); ok {
			f.clientSecret = s
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// POST /admin/realms/freecloud/clients/svc-uuid/client-secret (RegenerateClientSecret)
	if p == "/admin/realms/freecloud/clients/svc-uuid/client-secret" && m == http.MethodPost {
		atomic.AddInt32(&f.clientSecretRegenCalls, 1)
		newSecret := "regen-secret-" + strings.Repeat("x", 8)
		f.clientSecret = newSecret
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"type": "secret", "value": newSecret})
		return
	}

	// GET /admin/realms/freecloud/users (service account / demo / e2e-admin lookup)
	if (p == "/admin/realms/freecloud/users" || p == "/admin/realms/freecloud/users/") && m == http.MethodGet {
		username := r.URL.Query().Get("username")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(username, "service-account-"):
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "sa-user-uuid", "username": username}})
		case username == "e2e-admin" && f.e2eAdminExists:
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "e2e-admin-uuid", "username": username}})
		default:
			_ = json.NewEncoder(w).Encode([]map[string]string{})
		}
		return
	}

	// POST /admin/realms/freecloud/users (demo user / e2e-admin create)
	if (p == "/admin/realms/freecloud/users" || p == "/admin/realms/freecloud/users/") && m == http.MethodPost {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if username, ok := body["username"].(string); ok && username == "e2e-admin" {
			f.e2eAdminExists = true
			atomic.AddInt32(&f.e2eAdminUserCreateCalls, 1)
			w.Header().Set("Location", f.srv.URL+"/admin/realms/freecloud/users/e2e-admin-uuid")
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.Header().Set("Location", f.srv.URL+"/admin/realms/freecloud/users/demo-uuid")
		w.WriteHeader(http.StatusCreated)
		return
	}

	// PUT /admin/realms/freecloud/users/e2e-admin-uuid/reset-password
	if p == "/admin/realms/freecloud/users/e2e-admin-uuid/reset-password" && m == http.MethodPut {
		atomic.AddInt32(&f.e2eAdminPasswordSetCalls, 1)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// GET/POST /admin/realms/freecloud/users/e2e-admin-uuid/role-mappings/realm
	if p == "/admin/realms/freecloud/users/e2e-admin-uuid/role-mappings/realm" {
		if m == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			if f.e2eAdminHasRole {
				_, _ = w.Write([]byte(`[{"id":"role-admin-id","name":"admin"}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
			return
		}
		if m == http.MethodPost {
			f.e2eAdminHasRole = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// PUT /admin/realms/freecloud/clients/dash-uuid (enable direct access grants)
	if p == "/admin/realms/freecloud/clients/dash-uuid" && m == http.MethodPut {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if dag, ok := body["directAccessGrantsEnabled"].(bool); ok {
			f.dashboardDirectGrants = dag
		}
		if _, ok := body["redirectUris"]; ok {
			f.dashboardRedirectURIs = stringSliceBody(body, "redirectUris")
			f.dashboardWebOrigins = stringSliceBody(body, "webOrigins")
			atomic.AddInt32(&f.dashboardRedirectUpdateCalls, 1)
		}
		atomic.AddInt32(&f.dashboardUpdateCalls, 1)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// GET /admin/realms/freecloud/users/sa-user-uuid/role-mappings/clients/rm-uuid
	if strings.HasSuffix(p, "/users/sa-user-uuid/role-mappings/clients/rm-uuid") {
		if m == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if m == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// GET realm-management roles
	if idx := strings.Index(p, "/clients/rm-uuid/roles/"); idx != -1 && m == http.MethodGet {
		name := p[idx+len("/clients/rm-uuid/roles/"):]
		if name != "" && !strings.Contains(name, "/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"role-` + name + `-id","name":"` + name + `"}`))
			return
		}
	}

	// GET /admin/realms/freecloud/authentication/flows
	if p == "/admin/realms/freecloud/authentication/flows" && m == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"browser-id","alias":"browser","builtIn":true}]`))
		return
	}

	// POST copy browser flow
	if strings.HasSuffix(p, "/authentication/flows/browser/copy") && m == http.MethodPost {
		w.WriteHeader(http.StatusCreated)
		return
	}

	// POST add posture execution (into the "forms" sub-flow — see
	// ensurePostureFlow's formsFlowAlias).
	if strings.HasSuffix(p, "/executions/execution") && m == http.MethodPost {
		w.WriteHeader(http.StatusCreated)
		return
	}

	// GET/PUT executions for the "forms" sub-flow (URL-encoded space in the
	// alias "browser-with-posture forms" — accept either encoding).
	if strings.HasSuffix(p, "/browser-with-posture%20forms/executions") || strings.HasSuffix(p, "/browser-with-posture forms/executions") {
		if m == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			// Two siblings at level 0 (Username Password Form + our posture
			// check) plus one nested at level 1 (inside a Conditional OTP
			// sub-flow), matching the real shape closely enough to exercise
			// the same-level counting logic in ensurePostureFlow.
			_, _ = w.Write([]byte(`[
				{"id":"exec-pwd","providerId":"auth-username-password-form","requirement":"REQUIRED","level":0},
				{"id":"exec-otp-nested","providerId":"auth-otp-form","requirement":"REQUIRED","level":1},
				{"id":"exec-1","providerId":"freecloud-posture-check","requirement":"DISABLED","level":0}
			]`))
			return
		}
		if m == http.MethodPut {
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}

	// POST lower-priority for the posture execution.
	if strings.HasSuffix(p, "/executions/exec-1/lower-priority") && m == http.MethodPost {
		atomic.AddInt32(&f.lowerPriorityCalls, 1)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Demo user password reset
	if strings.HasSuffix(p, "/reset-password") && m == http.MethodPut {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Error(w, "fake KC: unhandled: "+m+" "+p, http.StatusNotFound)
}

// TestBootstrap_RealmCreatedOnFirstRun verifies the realm is created when absent.
func TestBootstrap_RealmCreatedOnFirstRun(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = false
	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if f.realmCreateCalls != 1 {
		t.Errorf("expected 1 realm create call, got %d", f.realmCreateCalls)
	}
}

// TestBootstrap_RealmNotRecreatedOnSecondRun verifies idempotency — existing realm is not recreated.
func TestBootstrap_RealmNotRecreatedOnSecondRun(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if f.realmCreateCalls != 0 {
		t.Errorf("realm must not be re-created if it already exists, got %d create calls", f.realmCreateCalls)
	}
}

// TestBootstrap_GroupsNotDuplicated verifies existing groups are not re-created.
func TestBootstrap_GroupsNotDuplicated(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	for _, g := range []string{"Engineering", "Marketing", "Sales", "Operations"} {
		f.groups[g] = true
	}

	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if f.groupCreateCalls != 0 {
		t.Errorf("groups must not be re-created if they already exist, got %d create calls", f.groupCreateCalls)
	}
}

// TestBootstrap_ServiceClientCreatedOnFirstRun verifies the freecloud-service client is created.
func TestBootstrap_ServiceClientCreatedOnFirstRun(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = false

	result, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if f.clientCreateCalls != 1 {
		t.Errorf("expected 1 client create call, got %d", f.clientCreateCalls)
	}
	if result.ServiceAccountSecret == "" {
		t.Error("expected a non-empty service account secret")
	}
}

// TestBootstrap_SecretStableOnSecondRun verifies the service-account secret
// is NOT rotated on a second bootstrap run. The backend keeps the secret in
// memory for its Keycloak admin client, so rotating it on every boot
// invalidates the secret every other replica is already using (they then fail
// with 401 until they restart) — issue #166.
func TestBootstrap_SecretStableOnSecondRun(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "old-secret"

	result, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.ServiceAccountSecret == "" {
		t.Error("expected a non-empty service account secret on second run")
	}
	if result.ServiceAccountSecret != "old-secret" {
		t.Errorf("expected the existing secret to be reused, got %q", result.ServiceAccountSecret)
	}
	if f.clientSecretRegenCalls != 0 {
		t.Errorf("expected no secret regeneration on the second run, got %d", f.clientSecretRegenCalls)
	}
}

// TestBootstrap_PostureFlow_ReordersExecutionPastSiblings verifies the A3 fix:
// the posture-check execution is added inside the "forms" sub-flow and moved
// below its one REQUIRED sibling at the same level (Username Password Form)
// via lower-priority, so it never runs before a user is authenticated.
// Regression guard for the real bug this caught: a REQUIRED authenticator
// with requiresUser()==true placed before/alongside the credential form
// causes Keycloak to throw "requires user to be set... but user is not set
// yet" on every login attempt when POSTURE_CHECK_ENABLED=true.
func TestBootstrap_PostureFlow_ReordersExecutionPastSiblings(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// The fake's execution list has exactly one REQUIRED sibling at the same
	// level as the posture check (Username Password Form) plus one nested
	// execution at a deeper level that must NOT be counted — so exactly one
	// lower-priority call is expected, not two.
	if f.lowerPriorityCalls != 1 {
		t.Errorf("expected exactly 1 lower-priority call (past the one same-level sibling), got %d", f.lowerPriorityCalls)
	}
}

// TestBootstrap_SeedE2EAdmin_CreatesUserAndEnablesDirectGrant verifies the A1
// e2e-admin seam: when SeedE2EAdmin is set, bootstrap creates the admin user,
// grants the "admin" realm role, sets its password, and enables direct access
// grants on the dashboard client.
func TestBootstrap_SeedE2EAdmin_CreatesUserAndEnablesDirectGrant(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "svc-secret"

	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
		SeedE2EAdmin:  true,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if f.e2eAdminUserCreateCalls != 1 {
		t.Errorf("expected 1 e2e-admin user create call, got %d", f.e2eAdminUserCreateCalls)
	}
	if f.e2eAdminPasswordSetCalls != 1 {
		t.Errorf("expected 1 e2e-admin password set call, got %d", f.e2eAdminPasswordSetCalls)
	}
	if !f.e2eAdminHasRole {
		t.Error("expected e2e-admin to be granted the admin realm role")
	}
	if !f.dashboardDirectGrants {
		t.Error("expected direct access grants to be enabled on the dashboard client")
	}
}

// TestBootstrap_SeedE2EAdmin_Idempotent verifies a second run does not
// re-create the user or re-grant the role, but does re-sync the password.
func TestBootstrap_SeedE2EAdmin_Idempotent(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "svc-secret"
	f.e2eAdminExists = true
	f.e2eAdminHasRole = true
	f.dashboardDirectGrants = true

	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
		SeedE2EAdmin:  true,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if f.e2eAdminUserCreateCalls != 0 {
		t.Errorf("expected e2e-admin user not to be re-created, got %d create calls", f.e2eAdminUserCreateCalls)
	}
	if f.e2eAdminPasswordSetCalls != 1 {
		t.Errorf("expected password to be (re-)synced exactly once, got %d calls", f.e2eAdminPasswordSetCalls)
	}
	if f.dashboardUpdateCalls != 0 {
		t.Errorf("expected dashboard client not to be updated when direct grants already enabled, got %d calls", f.dashboardUpdateCalls)
	}
}

// TestBootstrap_SeedE2EAdminDisabled_NoAdminUserCreated is the fail-closed
// regression guard: when SeedE2EAdmin is false (the default), bootstrap must
// never create the e2e-admin user or touch the dashboard client's grant flags.
func TestBootstrap_SeedE2EAdminDisabled_NoAdminUserCreated(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "svc-secret"

	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
		// SeedE2EAdmin intentionally omitted (false).
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if f.e2eAdminUserCreateCalls != 0 {
		t.Errorf("SeedE2EAdmin=false must never create the e2e-admin user, got %d create calls", f.e2eAdminUserCreateCalls)
	}
	if f.dashboardUpdateCalls != 0 {
		t.Errorf("SeedE2EAdmin=false must never touch the dashboard client, got %d update calls", f.dashboardUpdateCalls)
	}
}

// TestBootstrap_NewRealmGetsShortAccessTokenLifespan is the H8
// defense-in-depth guard: a freshly-created realm must not rely on
// Keycloak's install default access-token lifetime.
func TestBootstrap_NewRealmGetsShortAccessTokenLifespan(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = false
	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if f.createdAccessTokenLifespan == nil || *f.createdAccessTokenLifespan != accessTokenLifespanSeconds {
		t.Errorf("expected new realm created with accessTokenLifespan=%d, got %v", accessTokenLifespanSeconds, f.createdAccessTokenLifespan)
	}
}

// TestBootstrap_ExistingRealmAccessTokenLifespanBackfilled verifies a realm
// created before this change (no accessTokenLifespan set, simulating
// Keycloak's install default) gets updated on the next bootstrap run.
func TestBootstrap_ExistingRealmAccessTokenLifespanBackfilled(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.realmAccessTokenLifespan = nil // field absent, as an old realm would be

	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if atomic.LoadInt32(&f.realmUpdateCallsWithLifespan) != 1 {
		t.Fatalf("expected exactly 1 realm update call carrying accessTokenLifespan, got %d", f.realmUpdateCallsWithLifespan)
	}
	if f.updatedAccessTokenLifespan == nil || *f.updatedAccessTokenLifespan != accessTokenLifespanSeconds {
		t.Errorf("expected realm updated with accessTokenLifespan=%d, got %v", accessTokenLifespanSeconds, f.updatedAccessTokenLifespan)
	}
}

// TestBootstrap_ExistingRealmAccessTokenLifespanAlreadyCorrect_NoUpdate
// verifies idempotency: when the realm already carries the right lifespan,
// bootstrap must not issue a redundant UpdateRealm call.
func TestBootstrap_ExistingRealmAccessTokenLifespanAlreadyCorrect_NoUpdate(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	correct := accessTokenLifespanSeconds
	f.realmAccessTokenLifespan = &correct
	f.realmFields = hardenedRealmFields() // lifespan alone no longer means "already correct"

	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// Exactly 1 realm PUT is expected regardless — ensurePostureFlow always
	// PUTs the realm once (to bind browserFlow), independent of this fix. If
	// the lifespan check redundantly issued its own PUT here, this would be 2.
	if got := atomic.LoadInt32(&f.realmPutCalls); got != 1 {
		t.Errorf("expected exactly 1 realm PUT (from posture-flow binding only, no extra lifespan update), got %d", got)
	}
}

// hardenedRealmFields is the fully-hardened realm state ensureRealm must
// converge to, keyed by Keycloak's JSON field names.
func hardenedRealmFields() map[string]interface{} {
	return map[string]interface{}{
		"bruteForceProtected":          true,
		"failureFactor":                float64(30),
		"quickLoginCheckMilliSeconds":  float64(1000),
		"minimumQuickLoginWaitSeconds": float64(60),
		"waitIncrementSeconds":         float64(60),
		"maxFailureWaitSeconds":        float64(900),
		"maxDeltaTimeSeconds":          float64(43200),
		"passwordPolicy":               realmPasswordPolicy,
		"ssoSessionIdleTimeout":        float64(1800),
		"ssoSessionMaxLifespan":        float64(28800),
		"revokeRefreshToken":           true,
		"refreshTokenMaxReuse":         float64(0),
	}
}

func assertRealmHardened(t *testing.T, body map[string]interface{}, where string) {
	t.Helper()
	for k, want := range hardenedRealmFields() {
		got, ok := body[k]
		if !ok {
			t.Errorf("%s: realm body missing %q (want %v)", where, k, want)
			continue
		}
		if got != want {
			t.Errorf("%s: realm body %q = %v, want %v", where, k, got, want)
		}
	}
}

func runBootstrap(t *testing.T, f *fakeKC) {
	t.Helper()
	_, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

// TestBootstrap_NewRealmIsHardened verifies a freshly created realm carries
// brute-force protection, the password policy, bounded SSO lifespans and
// single-use refresh tokens instead of Keycloak's permissive defaults.
func TestBootstrap_NewRealmIsHardened(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()
	f.realmExists = false
	runBootstrap(t, f)
	if f.createdRealmBody == nil {
		t.Fatal("expected a realm creation call")
	}
	assertRealmHardened(t, f.createdRealmBody, "create")
}

// TestBootstrap_ExistingRealmHardeningBackfilled verifies a pre-existing
// realm at Keycloak defaults gets exactly one hardening UpdateRealm.
func TestBootstrap_ExistingRealmHardeningBackfilled(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()
	f.realmExists = true // all hardened fields absent, as an old realm would be
	runBootstrap(t, f)
	hardening := 0
	for _, body := range f.updatedRealmBodies {
		if body["bruteForceProtected"] == true {
			hardening++
			assertRealmHardened(t, body, "update")
		}
	}
	if hardening != 1 {
		t.Errorf("expected exactly 1 hardening realm update, got %d (total PUTs %d)", hardening, len(f.updatedRealmBodies))
	}
}

// TestBootstrap_AlreadyHardenedRealmNoUpdate verifies idempotency: a realm
// already carrying every hardened value triggers no hardening update (the
// only PUT left is ensurePostureFlow's browserFlow binding).
func TestBootstrap_AlreadyHardenedRealmNoUpdate(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()
	f.realmExists = true
	correct := accessTokenLifespanSeconds
	f.realmAccessTokenLifespan = &correct
	f.realmFields = hardenedRealmFields()
	runBootstrap(t, f)
	// The posture-flow PUT echoes the whole realm (hardened fields included),
	// so the PUT count is the signal: a redundant hardening update would make 2.
	if got := atomic.LoadInt32(&f.realmPutCalls); got != 1 {
		t.Errorf("expected exactly 1 realm PUT (posture-flow binding only), got %d", got)
	}
}

// TestBootstrap_SecretOverridePinned verifies the override secret is used when set.
func TestBootstrap_SecretOverridePinned(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = false

	const pinned = "my-pinned-secret"
	result, err := Run(context.Background(), Config{
		KeycloakURL:                  f.srv.URL,
		AdminUsername:                "admin",
		AdminPassword:                "admin",
		TargetRealm:                  "freecloud",
		ServiceAccountSecretOverride: pinned,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.ServiceAccountSecret != pinned {
		t.Errorf("expected pinned secret %q, got %q", pinned, result.ServiceAccountSecret)
	}
}

// TestBootstrap_KCAdminAPIScope_AssignedAsDefaultWhenPresent verifies that
// when the realm has the built-in kc-admin-api client scope (Keycloak
// 26.8.2+), bootstrap assigns it as a DEFAULT scope of freecloud-service,
// and that re-running bootstrap is a no-op success (idempotent). Without
// the scope the service-account token lacks the admin audience and every
// runtime Admin REST call fails with 401 on KC 26.8.2.
func TestBootstrap_KCAdminAPIScope_AssignedAsDefaultWhenPresent(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "old-secret"
	f.kcAdminAPIScopeExists = true

	cfg := Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	}
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("first Run failed: %v", err)
	}
	if !f.kcAdminAPIDefaultAssigned {
		t.Error("expected kc-admin-api to be assigned as a default client scope of freecloud-service")
	}
	if got := atomic.LoadInt32(&f.addDefaultScopeCalls); got != 1 {
		t.Errorf("expected exactly 1 add-default-scope call, got %d", got)
	}

	// Second run must succeed without re-assigning (already a default scope).
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("second Run failed: %v", err)
	}
	if got := atomic.LoadInt32(&f.addDefaultScopeCalls); got != 1 {
		t.Errorf("expected no additional add-default-scope call on re-run, got %d total", got)
	}
}

// TestBootstrap_KCAdminAPIScope_AbsentIsNoOp verifies backward compatibility:
// on Keycloak < 26.8.2 the realm has no kc-admin-api client scope, and
// bootstrap must succeed without assigning anything.
func TestBootstrap_KCAdminAPIScope_AbsentIsNoOp(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "old-secret"
	f.kcAdminAPIScopeExists = false

	if _, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got := atomic.LoadInt32(&f.addDefaultScopeCalls); got != 0 {
		t.Errorf("expected no add-default-scope calls when kc-admin-api does not exist, got %d", got)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// A Keycloak client with empty redirectUris rejects EVERY authorize
// request, so a fresh dashboard client must be created with the Auth.js
// callback for the configured dashboard origin.
func TestBootstrap_DashboardClientCreatedWithRedirectURIs(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "old-secret"
	f.dashboardAbsent = true

	if _, err := Run(context.Background(), Config{
		KeycloakURL:        f.srv.URL,
		AdminUsername:      "admin",
		AdminPassword:      "admin",
		TargetRealm:        "freecloud",
		DashboardPublicURL: "http://localhost:3000",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got := atomic.LoadInt32(&f.dashboardCreateCalls); got != 1 {
		t.Fatalf("expected dashboard client created once, got %d", got)
	}
	if !containsString(f.dashboardRedirectURIs, "http://localhost:3000/api/auth/callback/keycloak") {
		t.Errorf("expected dashboard redirectUris to contain the Auth.js callback, got %v", f.dashboardRedirectURIs)
	}
	if !containsString(f.dashboardWebOrigins, "http://localhost:3000") {
		t.Errorf("expected dashboard webOrigins to contain the dashboard origin, got %v", f.dashboardWebOrigins)
	}
}

// Deployments bootstrapped before redirect URIs were stamped have a
// dashboard client that already exists with none; the next boot must
// backfill them (once), preserving idempotency afterwards.
func TestBootstrap_DashboardRedirectURIsBackfilledOnExistingClient(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "old-secret"
	// Dashboard client exists (default) with no redirect URIs.

	cfg := Config{
		KeycloakURL:        f.srv.URL,
		AdminUsername:      "admin",
		AdminPassword:      "admin",
		TargetRealm:        "freecloud",
		DashboardPublicURL: "https://app.example.com",
	}
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("first Run failed: %v", err)
	}
	if !containsString(f.dashboardRedirectURIs, "https://app.example.com/api/auth/callback/keycloak") {
		t.Errorf("expected backfilled redirectUris to contain the Auth.js callback, got %v", f.dashboardRedirectURIs)
	}
	if got := atomic.LoadInt32(&f.dashboardRedirectUpdateCalls); got != 1 {
		t.Fatalf("expected exactly 1 redirect-URI update, got %d", got)
	}
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("second Run failed: %v", err)
	}
	if got := atomic.LoadInt32(&f.dashboardRedirectUpdateCalls); got != 1 {
		t.Errorf("expected no further redirect-URI update on re-run, got %d total", got)
	}
}

// Without a configured dashboard origin (e.g. e2e ROPC) bootstrap must
// not stamp anything — and must not fail.
func TestBootstrap_DashboardRedirectURIsSkippedWithoutPublicURL(t *testing.T) {
	f := newFakeKC(t)
	defer f.srv.Close()

	f.realmExists = true
	f.clientExists = true
	f.clientSecret = "old-secret"
	f.dashboardAbsent = true

	if _, err := Run(context.Background(), Config{
		KeycloakURL:   f.srv.URL,
		AdminUsername: "admin",
		AdminPassword: "admin",
		TargetRealm:   "freecloud",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(f.dashboardRedirectURIs) != 0 {
		t.Errorf("expected no redirectUris without a dashboard origin, got %v", f.dashboardRedirectURIs)
	}
}
