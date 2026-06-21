package handlers

// Tests for B1: SCIM discovery endpoints (ServiceProviderConfig, ResourceTypes, Schemas).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSCIMServiceProviderConfig(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/ServiceProviderConfig", nil)
	rec := httptest.NewRecorder()
	h.SCIMServiceProviderConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != scimContentType {
		t.Errorf("expected Content-Type %q, got %q", scimContentType, ct)
	}

	var cfg scimSPConfig
	if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Must include the SPC schema
	if len(cfg.Schemas) == 0 || cfg.Schemas[0] != scimSPCSchema {
		t.Errorf("expected schemas[0]=%q, got %v", scimSPCSchema, cfg.Schemas)
	}
	// PATCH must be supported
	if !cfg.Patch.Supported {
		t.Error("expected patch.supported=true")
	}
	// Bulk must be unsupported
	if cfg.Bulk.Supported {
		t.Error("expected bulk.supported=false")
	}
	// Filter must be supported with reasonable maxResults
	if !cfg.Filter.Supported {
		t.Error("expected filter.supported=true")
	}
	if cfg.Filter.MaxResults <= 0 {
		t.Errorf("expected filter.maxResults > 0, got %d", cfg.Filter.MaxResults)
	}
	// Sort must be unsupported
	if cfg.Sort.Supported {
		t.Error("expected sort.supported=false")
	}
	// ETag must be supported
	if !cfg.ETag.Supported {
		t.Error("expected etag.supported=true")
	}
	// At least one auth scheme
	if len(cfg.AuthenticationSchemes) == 0 {
		t.Error("expected at least one authenticationScheme")
	}
}

func TestSCIMResourceTypes(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/ResourceTypes", nil)
	rec := httptest.NewRecorder()
	h.SCIMResourceTypes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp scimResourceTypesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.TotalResults != 2 {
		t.Errorf("expected 2 resource types, got %d", resp.TotalResults)
	}

	tests := []struct {
		name     string
		endpoint string
		schema   string
	}{
		{"User", "/scim/v2/Users", scimUserSchema},
		{"Group", "/scim/v2/Groups", scimGroupSchema},
	}
	for i, tt := range tests {
		if i >= len(resp.Resources) {
			t.Fatalf("missing resource type at index %d", i)
		}
		rt := resp.Resources[i]
		if rt.Name != tt.name {
			t.Errorf("[%d] expected name=%q, got %q", i, tt.name, rt.Name)
		}
		if rt.Endpoint != tt.endpoint {
			t.Errorf("[%d] expected endpoint=%q, got %q", i, tt.endpoint, rt.Endpoint)
		}
		if rt.Schema != tt.schema {
			t.Errorf("[%d] expected schema=%q, got %q", i, tt.schema, rt.Schema)
		}
	}
}

func TestSCIMSchemas(t *testing.T) {
	h := setupTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Schemas", nil)
	rec := httptest.NewRecorder()
	h.SCIMSchemas(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp scimSchemasResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// We expect 5 schemas: User, Group, SPC, ResourceType, Schema
	if resp.TotalResults < 5 {
		t.Errorf("expected at least 5 schemas, got %d", resp.TotalResults)
	}

	// Index by schema ID
	byID := map[string]scimSchemaDefinition{}
	for _, s := range resp.Resources {
		byID[s.ID] = s
	}

	wantIDs := []string{
		scimUserSchema,
		scimGroupSchema,
		scimSPCSchema,
		scimResourceTypeSchema,
		scimSchemaSchema,
	}
	for _, id := range wantIDs {
		if _, ok := byID[id]; !ok {
			t.Errorf("missing schema ID %q", id)
		}
	}

	// User schema must have userName as required attribute
	userSchema := byID[scimUserSchema]
	foundUserName := false
	for _, attr := range userSchema.Attributes {
		if attr.Name == "userName" {
			foundUserName = true
			if !attr.Required {
				t.Error("userName attribute must be required")
			}
			break
		}
	}
	if !foundUserName {
		t.Error("user schema missing userName attribute")
	}

	// Group schema must have displayName as required attribute
	groupSchema := byID[scimGroupSchema]
	foundDisplayName := false
	for _, attr := range groupSchema.Attributes {
		if attr.Name == "displayName" {
			foundDisplayName = true
			if !attr.Required {
				t.Error("displayName attribute must be required")
			}
			break
		}
	}
	if !foundDisplayName {
		t.Error("group schema missing displayName attribute")
	}
}

func TestSCIMDiscoveryContentType(t *testing.T) {
	h := setupTestHandler(t)
	endpoints := []struct {
		path    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"/scim/v2/ServiceProviderConfig", h.SCIMServiceProviderConfig},
		{"/scim/v2/ResourceTypes", h.SCIMResourceTypes},
		{"/scim/v2/Schemas", h.SCIMSchemas},
	}
	for _, e := range endpoints {
		req := httptest.NewRequest(http.MethodGet, e.path, nil)
		rec := httptest.NewRecorder()
		e.handler(rec, req)
		if ct := rec.Header().Get("Content-Type"); ct != scimContentType {
			t.Errorf("%s: expected Content-Type=%q, got %q", e.path, scimContentType, ct)
		}
	}
}
