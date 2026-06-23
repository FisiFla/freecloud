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
		w.Header().Set("Location", f.srv.URL+"/admin/realms/freecloud")
		w.WriteHeader(http.StatusCreated)
		return
	}

	// GET/PUT /admin/realms/freecloud (exact realm)
	if p == "/admin/realms/freecloud" {
		if m == http.MethodGet {
			if f.realmExists {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"realm":"freecloud","enabled":true,"browserFlow":"browser"}`))
			} else {
				http.Error(w, `{"error":"Realm not found."}`, http.StatusNotFound)
			}
			return
		}
		if m == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
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
				_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "dash-uuid", "clientId": "freecloud-dashboard"}})
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
		newSecret := "regen-secret-" + strings.Repeat("x", 8)
		f.clientSecret = newSecret
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"type": "secret", "value": newSecret})
		return
	}

	// GET /admin/realms/freecloud/users (service account lookup + demo user check)
	if (p == "/admin/realms/freecloud/users" || p == "/admin/realms/freecloud/users/") && m == http.MethodGet {
		username := r.URL.Query().Get("username")
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(username, "service-account-") {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "sa-user-uuid", "username": username}})
		} else {
			_ = json.NewEncoder(w).Encode([]map[string]string{})
		}
		return
	}

	// POST /admin/realms/freecloud/users (demo user create)
	if (p == "/admin/realms/freecloud/users" || p == "/admin/realms/freecloud/users/") && m == http.MethodPost {
		w.Header().Set("Location", f.srv.URL+"/admin/realms/freecloud/users/demo-uuid")
		w.WriteHeader(http.StatusCreated)
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
	if strings.HasSuffix(p, "/clients/rm-uuid/roles/manage-users") && m == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"role-mu-id","name":"manage-users"}`))
		return
	}
	if strings.HasSuffix(p, "/clients/rm-uuid/roles/manage-clients") && m == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"role-mc-id","name":"manage-clients"}`))
		return
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

	// POST add posture execution
	if strings.HasSuffix(p, "/executions/execution") && m == http.MethodPost {
		w.WriteHeader(http.StatusCreated)
		return
	}

	// GET/PUT executions for posture flow
	if strings.HasSuffix(p, "/browser-with-posture/executions") {
		if m == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"exec-1","providerId":"freecloud-posture-check","requirement":"DISABLED"}]`))
			return
		}
		if m == http.MethodPut {
			w.WriteHeader(http.StatusAccepted)
			return
		}
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

// TestBootstrap_SecretReturnedOnSecondRun verifies the regenerated secret is returned when client exists.
func TestBootstrap_SecretReturnedOnSecondRun(t *testing.T) {
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
	if result.ServiceAccountSecret == "old-secret" {
		t.Error("expected secret to be regenerated, but got the old secret")
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
