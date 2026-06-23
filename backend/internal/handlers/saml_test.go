package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// TestGetSAMLIdPInitiatedURL verifies the handler returns the URL from Keycloak.
func TestGetSAMLIdPInitiatedURL(t *testing.T) {
	const appID = "app-saml-001"
	const kcClientID = "kc-saml-client"
	const wantURL = "https://keycloak.local/realms/freecloud/protocol/saml/clients/my-app"

	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = kcClientID
				*(dest[1].(*string)) = "SAML"
				return nil
			}}
		},
	}
	kc := &fakeKeycloak{
		getSAMLIdPInitiatedURLFn: func(_ context.Context, keycloakClientID string) (string, error) {
			if keycloakClientID != kcClientID {
				t.Errorf("expected keycloakClientID=%q, got %q", kcClientID, keycloakClientID)
			}
			return wantURL, nil
		},
	}

	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+appID+"/saml/idp-url", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("appId", appID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rec := httptest.NewRecorder()

	h.GetSAMLIdPInitiatedURL(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !env.Success {
		t.Fatal("expected success=true")
	}
	if env.Data.URL != wantURL {
		t.Errorf("url: got %q, want %q", env.Data.URL, wantURL)
	}
}

// TestGetSAMLIdPInitiatedURLNotSAML verifies that requesting the IdP URL for an OIDC
// app returns 400.
func TestGetSAMLIdPInitiatedURLNotSAML(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "kc-oidc-client"
				*(dest[1].(*string)) = "OIDC"
				return nil
			}}
		},
	}

	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-oidc/saml/idp-url", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("appId", "app-oidc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rec := httptest.NewRecorder()

	h.GetSAMLIdPInitiatedURL(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for OIDC app, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestGetSAMLMetadata verifies the handler returns metadata XML with correct content type.
func TestGetSAMLMetadata(t *testing.T) {
	const appID = "app-saml-002"
	const stubXML = `<?xml version="1.0" encoding="UTF-8"?><EntityDescriptor entityID="https://keycloak.local/realms/freecloud"/>`

	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "SAML"
				return nil
			}}
		},
	}
	kc := &fakeKeycloak{
		getSAMLMetadataXMLFn: func(_ context.Context) (string, error) {
			return stubXML, nil
		},
	}

	h := NewHandler(db, kc, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+appID+"/saml/metadata", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("appId", appID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rec := httptest.NewRecorder()

	h.GetSAMLMetadata(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("expected Content-Type application/xml, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "EntityDescriptor") {
		t.Errorf("expected XML body with EntityDescriptor, got: %s", rec.Body.String())
	}
}

// TestGetSAMLMetadataNotSAML verifies 400 for OIDC apps.
func TestGetSAMLMetadataNotSAML(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(_ context.Context, sql string, _ ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				*(dest[0].(*string)) = "OIDC"
				return nil
			}}
		},
	}

	h := NewHandler(db, &fakeKeycloak{}, &fakeFleet{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-oidc/saml/metadata", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("appId", "app-oidc")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rec := httptest.NewRecorder()

	h.GetSAMLMetadata(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for OIDC app, got %d: %s", rec.Code, rec.Body.String())
	}
}
