package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nerzal/gocloak/v13"
	"go.uber.org/zap"

	"github.com/FisiFla/freecloud/backend/internal/middleware"
)

// TestListIdentityProviders_OK verifies that ListIdentityProviders returns providers
// from Keycloak wrapped in the standard envelope.
func TestListIdentityProviders_OK(t *testing.T) {
	alias := "github"
	displayName := "GitHub"
	providerID := "github"
	enabled := true
	kc := &fakeKeycloak{
		listIdentityProvidersFn: func(_ context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
			return []*gocloak.IdentityProviderRepresentation{
				{Alias: &alias, DisplayName: &displayName, ProviderID: &providerID, Enabled: &enabled},
			}, nil
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/identity-providers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success bool        `json:"success"`
		Data    interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

// TestListIdentityProviders_KCError verifies 500 on Keycloak failure.
func TestListIdentityProviders_KCError(t *testing.T) {
	kc := &fakeKeycloak{
		listIdentityProvidersFn: func(_ context.Context) ([]*gocloak.IdentityProviderRepresentation, error) {
			return nil, fmt.Errorf("keycloak unavailable")
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/identity-providers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestCreateIdentityProvider_OK verifies that CreateIdentityProvider calls Keycloak
// and returns 201 with the alias.
func TestCreateIdentityProvider_OK(t *testing.T) {
	var capturedAlias string
	var capturedType string
	kc := &fakeKeycloak{
		createIdentityProviderFn: func(_ context.Context, alias, displayName, providerType string, config map[string]string) error {
			capturedAlias = alias
			capturedType = providerType
			return nil
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	body := `{"alias":"github","displayName":"GitHub","providerType":"github","config":{"clientId":"abc","clientSecret":"xyz"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/identity-providers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedAlias != "github" {
		t.Errorf("alias: want github, got %q", capturedAlias)
	}
	if capturedType != "github" {
		t.Errorf("providerType: want github, got %q", capturedType)
	}
}

// TestCreateIdentityProvider_InvalidType verifies 400 for unknown provider type.
func TestCreateIdentityProvider_InvalidType(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	body := `{"alias":"sso","providerType":"unknown_type"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/identity-providers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateIdentityProvider_MissingAlias verifies 400 when alias is empty.
func TestCreateIdentityProvider_MissingAlias(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, nil, zap.NewNop())
	r := newTestRouter(h)

	body := `{"alias":"","providerType":"google"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/identity-providers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateIdentityProvider_KCError verifies 500 on Keycloak failure.
func TestCreateIdentityProvider_KCError(t *testing.T) {
	kc := &fakeKeycloak{
		createIdentityProviderFn: func(_ context.Context, alias, displayName, providerType string, config map[string]string) error {
			return fmt.Errorf("keycloak error")
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	body := `{"alias":"google","providerType":"google"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/identity-providers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestUpdateIdentityProvider_OK verifies that UpdateIdentityProvider calls Keycloak.
func TestUpdateIdentityProvider_OK(t *testing.T) {
	var capturedAlias string
	kc := &fakeKeycloak{
		updateIdentityProviderFn: func(_ context.Context, alias, displayName string, config map[string]string) error {
			capturedAlias = alias
			return nil
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	body := `{"displayName":"GitHub Updated","config":{}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/identity-providers/github", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedAlias != "github" {
		t.Errorf("alias: want github, got %q", capturedAlias)
	}
}

// TestDeleteIdentityProvider_OK verifies that DeleteIdentityProvider calls Keycloak.
func TestDeleteIdentityProvider_OK(t *testing.T) {
	var capturedAlias string
	kc := &fakeKeycloak{
		deleteIdentityProviderFn: func(_ context.Context, alias string) error {
			capturedAlias = alias
			return nil
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/identity-providers/google", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedAlias != "google" {
		t.Errorf("alias: want google, got %q", capturedAlias)
	}
}

// TestDeleteIdentityProvider_KCError verifies 500 on Keycloak failure.
func TestDeleteIdentityProvider_KCError(t *testing.T) {
	kc := &fakeKeycloak{
		deleteIdentityProviderFn: func(_ context.Context, alias string) error {
			return fmt.Errorf("keycloak error")
		},
	}
	h := NewHandler(nil, kc, nil, zap.NewNop())
	r := newTestRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/identity-providers/google", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestIdentityProviders_PermissionGated verifies that non-super-admin gets 403.
func TestIdentityProviders_PermissionGated(t *testing.T) {
	h := NewHandler(nil, &fakeKeycloak{}, nil, zap.NewNop())
	r := newRoleTestRouter(h, middleware.RoleEndUser)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/settings/identity-providers"},
		{http.MethodPost, "/api/v1/settings/identity-providers"},
		{http.MethodPut, "/api/v1/settings/identity-providers/google"},
		{http.MethodDelete, "/api/v1/settings/identity-providers/google"},
	}
	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: expected 403, got %d", rt.method, rt.path, rec.Code)
		}
	}
}
