// Failing-first tests for the federation/LDAP hardening (security lane L2,
// issue 3): connectionUrl validation (scheme, userinfo, address blocks),
// vendor allow-list, and the LDAP_ALLOWED_HOSTS destination binding for the
// global LDAP_BIND_PASSWORD.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// federationRowQueryFn serves one federation_sources row (vendored config
// JSON) for any non-ownership query.
func federationRowQueryFn(vendor, connectionURL string) func(context.Context, string, ...any) pgx.Row {
	config := `{"connectionUrl":` + quoteJSON(connectionURL) +
		`,"bindDn":"cn=svc","usersDn":"ou=users"}`
	return ownershipFoundQueryRowFn(func(_ context.Context, _ string, _ ...any) pgx.Row {
		return fakeRow{scanFn: func(dest ...any) error {
			switch len(dest) {
			case 3:
				// Test path: id, component id, config.
				*(dest[0].(*string)) = federationID
				*(dest[1].(*string)) = federationComponentID
				*(dest[2].(*string)) = config
				return nil
			}
			vals := []any{
				federationID, "Corp AD", "ldap", vendor, config,
				federationComponentID, "", "",
			}
			for i, d := range dest {
				if i >= len(vals) {
					break
				}
				if p, ok := d.(*string); ok {
					if v, ok2 := vals[i].(string); ok2 {
						*p = v
					}
				}
			}
			return nil
		}}
	})
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestCreateFederationSource_RejectsBadConnectionURL(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	cases := []string{
		"http://dc.example.com",               // wrong scheme
		"https://dc.example.com",              // wrong scheme
		"notaurl",                             // unparseable
		"ldap://",                             // empty host
		"ldap://admin:secret@dc.example.com/", // userinfo smuggles a decoy host
		"ldap://dc.example.com:99999",         // bad port
		"ldap://dc.example.com:0",             // bad port
		"ldap://dc.example.com/#frag",         // fragment has no LDAP meaning
		"ldap://dc exam ple.com",              // whitespace
		"ldaps://[::1",                        // broken IPv6 literal
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			called := false
			kc := &fakeKeycloak{
				createFederationComponentFn: func(_ context.Context, _, _, _, _, _, _ string) (string, error) {
					called = true
					return "kc-comp", nil
				},
			}
			db := &fakeDB{
				execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
					return pgconn.CommandTag{}, nil
				},
				queryRowFn: federationRowQueryFn("ad", raw),
			}
			h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
			h.SetLDAPBindPassword("bind-secret")

			body, _ := json.Marshal(map[string]string{
				"name": "Corp AD", "vendor": "ad", "connectionUrl": raw,
				"bindDn": "cn=svc", "usersDn": "ou=users",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
			req = withOrgContext(req)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.CreateFederationSource(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("connectionUrl=%q: expected 400, got %d: %s", raw, rec.Code, rec.Body.String())
			}
			if called {
				t.Fatalf("connectionUrl=%q: Keycloak must not receive an invalid URL", raw)
			}
		})
	}
}

func TestCreateFederationSource_ProductionRequiresLDAPS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("LDAP_ALLOWED_HOSTS", "dc.example.com")
	newHandler := func(kc *fakeKeycloak) (*Handler, *fakeDB) {
		db := &fakeDB{
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
			queryRowFn: federationRowQueryFn("ad", "ldaps://dc.example.com"),
		}
		h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
		h.SetLDAPBindPassword("bind-secret")
		return h, db
	}
	post := func(h *Handler, raw string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{
			"name": "Corp AD", "vendor": "ad", "connectionUrl": raw,
			"bindDn": "cn=svc", "usersDn": "ou=users",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
		req = withOrgContext(req)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateFederationSource(rec, req)
		return rec
	}

	called := false
	kc := &fakeKeycloak{
		createFederationComponentFn: func(_ context.Context, _, _, _, _, _, _ string) (string, error) {
			called = true
			return "kc-comp", nil
		},
	}
	h, _ := newHandler(kc)
	if rec := post(h, "ldap://dc.example.com"); rec.Code != http.StatusBadRequest {
		t.Fatalf("plain ldap in production: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("Keycloak must not receive a cleartext LDAP URL in production")
	}

	// ldaps to an allow-listed host works. The fixture hostname cannot
	// resolve, so production DNS validation would fail closed on it — use a
	// literal private address (corporate LDAP lives on private networks).
	t.Setenv("LDAP_ALLOWED_HOSTS", "10.0.0.5")
	h, _ = newHandler(kc)
	if rec := post(h, "ldaps://10.0.0.5"); rec.Code != http.StatusCreated {
		t.Fatalf("ldaps to private host: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateFederationSource_BlockedAddresses(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("LDAP_ALLOWED_HOSTS", "127.0.0.1,169.254.169.254,10.0.0.5,fd00::5")
	for _, tc := range []struct {
		url  string
		want int
	}{
		{"ldaps://127.0.0.1", http.StatusBadRequest},       // loopback
		{"ldaps://169.254.169.254", http.StatusBadRequest}, // link-local/metadata
		{"ldaps://[::1]", http.StatusBadRequest},           // IPv6 loopback
		{"ldaps://10.0.0.5", http.StatusCreated},           // RFC1918 stays allowed
		{"ldaps://[fd00::5]", http.StatusCreated},          // ULA stays allowed
	} {
		db := &fakeDB{
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
			queryRowFn: federationRowQueryFn("ad", tc.url),
		}
		h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
		h.SetLDAPBindPassword("bind-secret")
		body, _ := json.Marshal(map[string]string{
			"name": "Corp AD", "vendor": "ad", "connectionUrl": tc.url,
			"bindDn": "cn=svc", "usersDn": "ou=users",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
		req = withOrgContext(req)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateFederationSource(rec, req)
		if rec.Code != tc.want {
			t.Errorf("connectionUrl=%q: expected %d, got %d: %s", tc.url, tc.want, rec.Code, rec.Body.String())
		}
	}
}

func TestCreateFederationSource_VendorAllowList(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	for _, tc := range []struct {
		vendor string
		want   int
	}{
		{"ad", http.StatusCreated},
		{"rhds", http.StatusCreated},
		{"tivoli", http.StatusCreated},
		{"edirectory", http.StatusCreated},
		{"other", http.StatusCreated},
		{"", http.StatusCreated}, // defaults to "other"
		{"AD", http.StatusCreated},
		{"freeipa", http.StatusBadRequest},
		{"ldap'; DROP TABLE x;--", http.StatusBadRequest},
	} {
		called := false
		kc := &fakeKeycloak{
			createFederationComponentFn: func(_ context.Context, _, _, _, _, _, _ string) (string, error) {
				called = true
				return "kc-comp", nil
			},
		}
		db := &fakeDB{
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
			queryRowFn: federationRowQueryFn("ad", "ldap://dc.example.com"),
		}
		h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
		h.SetLDAPBindPassword("bind-secret")
		body, _ := json.Marshal(map[string]string{
			"name": "Corp AD", "vendor": tc.vendor, "connectionUrl": "ldap://dc.example.com",
			"bindDn": "cn=svc", "usersDn": "ou=users",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
		req = withOrgContext(req)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateFederationSource(rec, req)
		if rec.Code != tc.want {
			t.Errorf("vendor=%q: expected %d, got %d: %s", tc.vendor, tc.want, rec.Code, rec.Body.String())
		}
		if tc.want != http.StatusCreated && called {
			t.Errorf("vendor=%q: Keycloak must not receive a rejected vendor", tc.vendor)
		}
	}
}

func TestCreateFederationSource_AllowListFailClosed(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	post := func(t *testing.T, raw string) *httptest.ResponseRecorder {
		db := &fakeDB{
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
			queryRowFn: federationRowQueryFn("ad", raw),
		}
		h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
		h.SetLDAPBindPassword("bind-secret")
		body, _ := json.Marshal(map[string]string{
			"name": "Corp AD", "vendor": "ad", "connectionUrl": raw,
			"bindDn": "cn=svc", "usersDn": "ou=users",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
		req = withOrgContext(req)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateFederationSource(rec, req)
		return rec
	}

	// Unset allow-list in production: refuse with a clear message.
	t.Setenv("LDAP_ALLOWED_HOSTS", "")
	if rec := post(t, "ldaps://10.0.0.5"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unset allow-list: expected 400, got %d: %s", rec.Code, rec.Body.String())
	} else if !strings.Contains(rec.Body.String(), "LDAP_ALLOWED_HOSTS") {
		t.Fatalf("the 400 must name LDAP_ALLOWED_HOSTS: %s", rec.Body.String())
	}

	// Host not on the list: refuse.
	t.Setenv("LDAP_ALLOWED_HOSTS", "10.0.0.6")
	if rec := post(t, "ldaps://10.0.0.5"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unlisted host: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// Listed host: allow (case-insensitive, surrounding spaces tolerated).
	t.Setenv("LDAP_ALLOWED_HOSTS", " 10.0.0.9 , 10.0.0.5 ")
	if rec := post(t, "ldaps://10.0.0.5"); rec.Code != http.StatusCreated {
		t.Fatalf("listed host: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateFederationSource_DevTestUnrestricted(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("LDAP_ALLOWED_HOSTS", "")
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: federationRowQueryFn("ad", "ldap://localhost:1389"),
	}
	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
	h.SetLDAPBindPassword("bind-secret")
	body, _ := json.Marshal(map[string]string{
		"name": "E2E LDAP", "vendor": "other", "connectionUrl": "ldap://localhost:1389",
		"bindDn": "cn=admin,dc=example,dc=com", "usersDn": "ou=users",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources", bytes.NewReader(body))
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateFederationSource(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("dev/test without allow-list: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateFederationSource_RepointRequiresAllowList(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	patch := func(t *testing.T, storedURL, body string) *httptest.ResponseRecorder {
		db := &fakeDB{
			execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
				return pgconn.CommandTag{}, nil
			},
			queryRowFn: federationRowQueryFn("ad", storedURL),
		}
		h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())
		h.SetLDAPBindPassword("bind-secret")
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/federation/sources/"+federationID, bytes.NewReader([]byte(body)))
		req = withChiIDParam(req, federationID)
		req = withOrgContext(req)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.UpdateFederationSource(rec, req)
		return rec
	}

	// Repoint to a new host, allow-list unset: refuse.
	t.Setenv("LDAP_ALLOWED_HOSTS", "")
	if rec := patch(t, "ldaps://10.0.0.5", `{"connectionUrl":"ldaps://10.0.0.6"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("repoint without allow-list: expected 400, got %d: %s", rec.Code, rec.Body.String())
	} else if !strings.Contains(rec.Body.String(), "LDAP_ALLOWED_HOSTS") {
		t.Fatalf("the 400 must name LDAP_ALLOWED_HOSTS: %s", rec.Body.String())
	}

	// Same host, new port: not a new destination, no allow-list needed.
	if rec := patch(t, "ldaps://10.0.0.5", `{"connectionUrl":"ldaps://10.0.0.5:1636"}`); rec.Code != http.StatusOK {
		t.Fatalf("same-host change: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// New host on the allow-list: allow.
	t.Setenv("LDAP_ALLOWED_HOSTS", "10.0.0.6")
	if rec := patch(t, "ldaps://10.0.0.5", `{"connectionUrl":"ldaps://10.0.0.6"}`); rec.Code != http.StatusOK {
		t.Fatalf("listed repoint: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Invalid new URL: refuse before Keycloak.
	if rec := patch(t, "ldaps://10.0.0.5", `{"connectionUrl":"http://10.0.0.6"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid new URL: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// Invalid new vendor: refuse.
	if rec := patch(t, "ldaps://10.0.0.5", `{"vendor":"freeipa"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid new vendor: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateFederationSource_UnchangedLegacyURLPassesThrough(t *testing.T) {
	// A legacy cleartext row must still accept unrelated updates (rename):
	// validation applies to newly supplied values, and repointing to a new
	// host goes through the full gate.
	t.Setenv("APP_ENV", "production")
	t.Setenv("LDAP_ALLOWED_HOSTS", "")
	updated := false
	kc := &fakeKeycloak{
		updateFederationComponentFn: func(_ context.Context, _, _, _, _, _, _, _ string) error {
			updated = true
			return nil
		},
	}
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: federationRowQueryFn("ad", "ldap://10.0.0.5"),
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	h.SetLDAPBindPassword("bind-secret")
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/federation/sources/"+federationID, bytes.NewReader([]byte(`{"name":"Renamed"}`)))
	req = withChiIDParam(req, federationID)
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateFederationSource(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("name-only update of a legacy row: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !updated {
		t.Fatal("the update must still reach Keycloak")
	}
}

func TestTestFederationConnection_ValidatesStoredURL(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	db := &fakeDB{
		execFn: func(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
		queryRowFn: federationRowQueryFn("ad", "http://evil.example.com/steal"),
	}
	called := false
	kc := &fakeKeycloak{
		testLDAPConnectionFn: func(_ context.Context, _, _, _, _ string) error {
			called = true
			return nil
		},
	}
	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())
	h.SetLDAPBindPassword("bind-secret")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/sources/"+federationID+"/test", nil)
	req = withChiIDParam(req, federationID)
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.TestFederationConnection(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("stored non-LDAP URL: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("Keycloak must not receive an invalid stored URL")
	}
}
