package keycloak

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Security-fix lane L4, item 4: HasAdminUser must report whether AN ADMIN
// EXISTS — a member of the "admin" realm role — not whether ANY user
// exists. A SCIM-provisioned or demo user created before first-run setup
// must not brick POST /api/v1/setup with a permanent 409.
func TestL4HasAdminUserChecksRoleMembership(t *testing.T) {
	newRoleServer := func(t *testing.T, roleUsersStatus int, roleUsersBody string, paths *[]string) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*paths = append(*paths, r.URL.Path)
			if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"tok-123","expires_in":300,"token_type":"Bearer"}`))
				return
			}
			if strings.HasSuffix(r.URL.Path, "/roles/admin/users") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(roleUsersStatus)
				_, _ = w.Write([]byte(roleUsersBody))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
	}

	t.Run("admin member present", func(t *testing.T) {
		var paths []string
		srv := newRoleServer(t, http.StatusOK, `[{"id":"u1","username":"admin@example.com"}]`, &paths)
		defer srv.Close()
		k := NewClient(srv.URL, "client", "secret", "myrealm")
		got, err := k.HasAdminUser(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Error("expected true when the admin role has a member")
		}
		found := false
		for _, p := range paths {
			if strings.HasSuffix(p, "/roles/admin/users") {
				found = true
			}
			if strings.HasSuffix(p, "/users") && !strings.Contains(p, "/roles/") {
				t.Errorf("must not consult the any-user list endpoint, called %s", p)
			}
		}
		if !found {
			t.Errorf("expected a role-membership query, called paths: %v", paths)
		}
	})

	t.Run("no admin member", func(t *testing.T) {
		var paths []string
		srv := newRoleServer(t, http.StatusOK, `[]`, &paths)
		defer srv.Close()
		k := NewClient(srv.URL, "client", "secret", "myrealm")
		got, err := k.HasAdminUser(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Error("expected false when the admin role has no members")
		}
	})

	t.Run("admin role missing is not provisioned", func(t *testing.T) {
		var paths []string
		srv := newRoleServer(t, http.StatusNotFound, `{"error":"role not found"}`, &paths)
		defer srv.Close()
		k := NewClient(srv.URL, "client", "secret", "myrealm")
		got, err := k.HasAdminUser(context.Background())
		if err != nil {
			t.Fatalf("a missing admin role must not error, got: %v", err)
		}
		if got {
			t.Error("expected false when the admin role does not exist yet")
		}
	})

	t.Run("keycloak error fails closed", func(t *testing.T) {
		var paths []string
		srv := newRoleServer(t, http.StatusInternalServerError, `{"error":"boom"}`, &paths)
		defer srv.Close()
		k := NewClient(srv.URL, "client", "secret", "myrealm")
		if _, err := k.HasAdminUser(context.Background()); err == nil {
			t.Error("expected an error to surface (callers fail closed on it), got nil")
		}
	})
}

// Security-fix lane L4, item 8: RenameGroup must read-modify-write so the
// group's attributes (notably org_id, which scopes SCIM groups to their
// org) survive the rename instead of being wiped by an id+name-only PUT.
func TestL4RenameGroupPreservesAttributes(t *testing.T) {
	var gotGet bool
	var putBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok-123","expires_in":300,"token_type":"Bearer"}`))
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/groups/g1") {
			gotGet = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"g1","name":"Old","path":"/Old","attributes":{"org_id":["org1"],"team":["a"]}}`))
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/groups/g1") {
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &putBody); err != nil {
				t.Errorf("PUT body is not JSON: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	k := NewClient(srv.URL, "client", "secret", "myrealm")
	if err := k.RenameGroup(context.Background(), "g1", "New"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gotGet {
		t.Fatal("expected a GET of the current group before the PUT (read-modify-write)")
	}
	if putBody == nil {
		t.Fatal("expected a PUT of the group representation")
	}
	if putBody["name"] != "New" {
		t.Errorf("PUT name = %v, want New", putBody["name"])
	}
	attrs, _ := putBody["attributes"].(map[string]any)
	if attrs == nil {
		t.Fatalf("PUT dropped the group attributes: %v", putBody)
	}
	if org, _ := attrs["org_id"].([]any); len(org) != 1 || org[0] != "org1" {
		t.Errorf("PUT org_id = %v, want [org1] (must be preserved)", attrs["org_id"])
	}
	if team, _ := attrs["team"].([]any); len(team) != 1 || team[0] != "a" {
		t.Errorf("PUT team = %v, want [a] (must be preserved)", attrs["team"])
	}
}

func TestL4RenameGroupReadFailureFailsClosed(t *testing.T) {
	var gotPut bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok-123","expires_in":300,"token_type":"Bearer"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/groups/g1") {
			if r.Method == http.MethodPut {
				gotPut = true
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	k := NewClient(srv.URL, "client", "secret", "myrealm")
	if err := k.RenameGroup(context.Background(), "g1", "New"); err == nil {
		t.Error("expected an error when the group cannot be read, got nil")
	}
	if gotPut {
		t.Error("must not PUT a rename when the current representation could not be read")
	}
}

// Security-fix lane L4, item 11: RSA_SHA1 is removed from the SAML signing
// allow-list and rejected with a clear validation error (SHA-1 signatures
// are not accepted), instead of silently mapping to something else.
func TestL4SAMLAttributesRejectRSASHA1(t *testing.T) {
	for _, algo := range []string{"RSA_SHA1", "rsa_sha1"} {
		_, err := buildSAMLAttributes("app", "https://sp.example.com/acs", "https://sp.example.com", &SAMLOptions{SigningAlgorithm: algo})
		if err == nil {
			t.Errorf("algo %q: expected a validation error, got nil", algo)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), "sha-1") && !strings.Contains(strings.ToLower(err.Error()), "sha1") {
			t.Errorf("algo %q: error should name SHA-1, got %q", algo, err.Error())
		}
	}
	for _, algo := range []string{"", "RSA_SHA256", "RSA_SHA512"} {
		attrs, err := buildSAMLAttributes("app", "https://sp.example.com/acs", "https://sp.example.com", &SAMLOptions{SigningAlgorithm: algo})
		if err != nil {
			t.Errorf("algo %q: expected no error, got %v", algo, err)
			continue
		}
		want := algo
		if want == "" {
			want = "RSA_SHA256"
		}
		if attrs["saml.signature.algorithm"] != want {
			t.Errorf("algo %q: saml.signature.algorithm = %q, want %q", algo, attrs["saml.signature.algorithm"], want)
		}
	}
}
