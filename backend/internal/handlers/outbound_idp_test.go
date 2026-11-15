// Failing-first tests for the identity provider hardening (security lane
// L2, issue 11): secret-bearing config keys are redacted on list, an update
// sending the mask back preserves the real secret, and aliases are
// validated on create/update/delete.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nerzal/gocloak/v13"
	"go.uber.org/zap"
)

func idpPtr(s string) *string { return &s }

func TestListIdentityProviders_RedactsSecrets(t *testing.T) {
	cfg := map[string]string{
		"clientId":     "abc",
		"clientSecret": "real-secret",
		"apiToken":     "real-token",
		"dbPassword":   "real-password",
		"SECRETKEY":    "shout",
		"displayHint":  "keep-me",
	}
	kc := &fakeKeycloak{
		listIdentityProvidersFn: func(context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
			return []*gocloak.IdentityProviderRepresentation{
				{Alias: idpPtr("github"), DisplayName: idpPtr("GitHub"), Config: &cfg},
			}, nil
		},
	}
	h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/identity-providers", nil)
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.ListIdentityProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, leaked := range []string{"real-secret", "real-token", "real-password", "shout"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("response leaks a secret value %q: %s", leaked, body)
		}
	}
	if !strings.Contains(body, "********") {
		t.Fatalf("expected the fixed secret mask in the response: %s", body)
	}
	if !strings.Contains(body, "keep-me") || !strings.Contains(body, "abc") {
		t.Fatalf("non-secret config must pass through unredacted: %s", body)
	}
}

func TestUpdateIdentityProvider_MaskPreservesSecret(t *testing.T) {
	current := map[string]string{"clientId": "abc", "clientSecret": "real-secret"}
	var gotConfig map[string]string
	kc := &fakeKeycloak{
		listIdentityProvidersFn: func(context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
			return []*gocloak.IdentityProviderRepresentation{
				{Alias: idpPtr("github"), Config: &current},
			}, nil
		},
		updateIdentityProviderFn: func(_ context.Context, _ string, _ string, config map[string]string) error {
			gotConfig = config
			return nil
		},
	}
	h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())

	body := `{"displayName":"GitHub","config":{"clientId":"new-id","clientSecret":"********"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/identity-providers/github", bytes.NewReader([]byte(body)))
	req = chiCtxWithID(req, "alias", "github")
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateIdentityProvider(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotConfig["clientSecret"] != "real-secret" {
		t.Fatalf("clientSecret sent to Keycloak = %q: the mask must restore the stored secret, not overwrite it", gotConfig["clientSecret"])
	}
	if gotConfig["clientId"] != "new-id" {
		t.Fatalf("clientId = %q: non-secret values must pass through", gotConfig["clientId"])
	}
}

func TestUpdateIdentityProvider_MaskWithoutStoredSecretIsRejected(t *testing.T) {
	current := map[string]string{"clientId": "abc"} // no clientSecret stored
	called := false
	kc := &fakeKeycloak{
		listIdentityProvidersFn: func(context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
			return []*gocloak.IdentityProviderRepresentation{
				{Alias: idpPtr("github"), Config: &current},
			}, nil
		},
		updateIdentityProviderFn: func(_ context.Context, _ string, _ string, _ map[string]string) error {
			called = true
			return nil
		},
	}
	h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())

	body := `{"displayName":"GitHub","config":{"clientSecret":"********"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/identity-providers/github", bytes.NewReader([]byte(body)))
	req = chiCtxWithID(req, "alias", "github")
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateIdentityProvider(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mask with no stored secret: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("Keycloak must not be called when the mask cannot be resolved")
	}
}

func TestUpdateIdentityProvider_FreshSecretPassesThrough(t *testing.T) {
	listCalled := false
	var gotConfig map[string]string
	kc := &fakeKeycloak{
		listIdentityProvidersFn: func(context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
			listCalled = true
			return nil, nil
		},
		updateIdentityProviderFn: func(_ context.Context, _ string, _ string, config map[string]string) error {
			gotConfig = config
			return nil
		},
	}
	h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())

	body := `{"displayName":"GitHub","config":{"clientSecret":"brand-new-secret"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/identity-providers/github", bytes.NewReader([]byte(body)))
	req = chiCtxWithID(req, "alias", "github")
	req = withOrgContext(req)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateIdentityProvider(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotConfig["clientSecret"] != "brand-new-secret" {
		t.Fatalf("clientSecret = %q: a fresh secret must pass through", gotConfig["clientSecret"])
	}
	if listCalled {
		t.Error("no mask was submitted, so no Keycloak read-back is needed")
	}
}

func TestIdentityProvider_AliasValidation(t *testing.T) {
	long128 := strings.Repeat("a", 128)
	long129 := strings.Repeat("a", 129)
	cases := []struct {
		alias string
		want  int
	}{
		{"github", http.StatusCreated},
		{"my.idp-1", http.StatusCreated},
		{"UPPER_MIX", http.StatusCreated},
		{long128, http.StatusCreated},
		{long129, http.StatusBadRequest},
		{"../escape", http.StatusBadRequest},
		{"a/b", http.StatusBadRequest},
		{"has space", http.StatusBadRequest},
		{"semi;colon", http.StatusBadRequest},
		{"", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run("create:"+tc.alias, func(t *testing.T) {
			called := false
			kc := &fakeKeycloak{
				createIdentityProviderFn: func(_ context.Context, _ string, _ string, _ string, _ map[string]string) error {
					called = true
					return nil
				},
			}
			h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())
			body, _ := json.Marshal(map[string]any{
				"alias": tc.alias, "displayName": "X", "providerType": "github",
				"config": map[string]string{"clientId": "abc"},
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/identity-providers", bytes.NewReader(body))
			req = withOrgContext(req)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.CreateIdentityProvider(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("alias=%q: expected %d, got %d: %s", tc.alias, tc.want, rec.Code, rec.Body.String())
			}
			if tc.want != http.StatusCreated && called {
				t.Fatalf("alias=%q: Keycloak must not receive a rejected alias", tc.alias)
			}
		})
	}

	// Update and delete take the alias from the path: invalid values are
	// rejected before Keycloak is touched.
	for _, alias := range []string{"../escape", "a/b", long129} {
		updCalled, delCalled := false, false
		kc := &fakeKeycloak{
			updateIdentityProviderFn: func(_ context.Context, _ string, _ string, _ map[string]string) error {
				updCalled = true
				return nil
			},
			deleteIdentityProviderFn: func(_ context.Context, _ string) error {
				delCalled = true
				return nil
			},
		}
		h := NewHandler(&fakeDB{}, kc, &fakeFleet{}, zap.NewNop())

		req := httptest.NewRequest(http.MethodPut, "/x", bytes.NewReader([]byte(`{"displayName":"X"}`)))
		req = chiCtxWithID(req, "alias", alias)
		req = withOrgContext(req)
		rec := httptest.NewRecorder()
		h.UpdateIdentityProvider(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("update alias=%q: expected 400, got %d", alias, rec.Code)
		}

		req = httptest.NewRequest(http.MethodDelete, "/x", nil)
		req = chiCtxWithID(req, "alias", alias)
		req = withOrgContext(req)
		rec = httptest.NewRecorder()
		h.DeleteIdentityProvider(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("delete alias=%q: expected 400, got %d", alias, rec.Code)
		}
		if updCalled || delCalled {
			t.Fatalf("alias=%q: Keycloak must not receive a rejected alias", alias)
		}
	}
}
