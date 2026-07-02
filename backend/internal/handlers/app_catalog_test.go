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
)

// TestListAppTemplates verifies that the endpoint returns all static templates.
func TestListAppTemplates(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/templates", nil)
	rec := httptest.NewRecorder()
	h.ListAppTemplates(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}

	// resp.Data is []interface{} after JSON decode through APIResponse.
	list, ok := resp.Data.([]interface{})
	if !ok {
		t.Fatalf("expected data to be a list, got %T", resp.Data)
	}
	if len(list) != len(appTemplates) {
		t.Errorf("expected %d templates, got %d", len(appTemplates), len(list))
	}
}

// TestCreateAppFromTemplate_SAMLGoogleWorkspace verifies a successful SAML template creation.
func TestCreateAppFromTemplate_SAMLGoogleWorkspace(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = "app-uuid-001"
				}
				return nil
			}}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
	}

	h := setupTestHandler(t)
	h.db = db

	body := `{
		"name": "My Google Workspace",
		"fields": {
			"baseURL": "https://www.google.com/a/example.com",
			"acsURL":  "https://www.google.com/a/example.com/acs"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/templates/google-workspace/create",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "templateId", "google-workspace")
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.CreateAppFromTemplate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got: %s", rec.Body.String())
	}

	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, got %T", resp.Data)
	}
	if data["id"] != "app-uuid-001" {
		t.Errorf("expected id=app-uuid-001, got %v", data["id"])
	}
	if data["name"] != "My Google Workspace" {
		t.Errorf("expected name=My Google Workspace, got %v", data["name"])
	}
	// SAML metadata must be present.
	if _, ok := data["samlEntityId"]; !ok {
		t.Error("expected samlEntityId in response")
	}
}

// TestCreateAppFromTemplate_MissingRequiredField verifies 400 when a required field is absent.
func TestCreateAppFromTemplate_MissingRequiredField(t *testing.T) {
	h := setupTestHandler(t)
	h.db = &fakeDB{}

	// Omit "acsURL" which is required for google-workspace.
	body := `{
		"name": "Bad App",
		"fields": {
			"baseURL": "https://www.google.com/a/example.com"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/templates/google-workspace/create",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "templateId", "google-workspace")
	rec := httptest.NewRecorder()
	h.CreateAppFromTemplate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateAppFromTemplate_UnknownTemplateID verifies 404 for an unrecognised template ID.
func TestCreateAppFromTemplate_UnknownTemplateID(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"X","fields":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/templates/does-not-exist/create",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "templateId", "does-not-exist")
	rec := httptest.NewRecorder()
	h.CreateAppFromTemplate(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateAppFromTemplate_OIDCGeneric verifies a successful OIDC template creation.
func TestCreateAppFromTemplate_OIDCGeneric(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
			return fakeRow{scanFn: func(dest ...any) error {
				if s, ok := dest[0].(*string); ok {
					*s = "app-uuid-002"
				}
				return nil
			}}
		},
		execFn: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, nil
		},
	}

	h := setupTestHandler(t)
	h.db = db

	body := `{
		"name": "My OIDC App",
		"fields": {
			"baseURL":     "https://myapp.example.com",
			"redirectURI": "https://myapp.example.com/auth/callback"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/templates/generic-oidc/create",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "templateId", "generic-oidc")
	req = withOrgContext(req)
	rec := httptest.NewRecorder()
	h.CreateAppFromTemplate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got: %s", rec.Body.String())
	}
	data, _ := resp.Data.(map[string]interface{})
	if data["id"] != "app-uuid-002" {
		t.Errorf("expected id=app-uuid-002, got %v", data["id"])
	}
	// OIDC apps do not expose SAML metadata fields.
	if _, hasSAML := data["samlEntityId"]; hasSAML {
		t.Error("OIDC app should not have samlEntityId")
	}
}
