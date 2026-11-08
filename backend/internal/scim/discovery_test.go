package scim

// Tests for B1: SCIM discovery endpoints (ServiceProviderConfig, ResourceTypes, Schemas).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSCIMServiceProviderConfig(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/ServiceProviderConfig", nil)
	rec := httptest.NewRecorder()
	ServiceProviderConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("expected Content-Type %q, got %q", ContentType, ct)
	}

	var cfg SPConfig
	if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Must include the SPC schema
	if len(cfg.Schemas) == 0 || cfg.Schemas[0] != SchemaServiceProviderConfig {
		t.Errorf("expected schemas[0]=%q, got %v", SchemaServiceProviderConfig, cfg.Schemas)
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
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/ResourceTypes", nil)
	rec := httptest.NewRecorder()
	ResourceTypes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ResourceTypesResponse
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
		{"User", "/scim/v2/Users", SchemaUser},
		{"Group", "/scim/v2/Groups", SchemaGroup},
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
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Schemas", nil)
	rec := httptest.NewRecorder()
	Schemas(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp SchemasResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// We expect 5 schemas: User, Group, SPC, ResourceType, Schema
	if resp.TotalResults < 5 {
		t.Errorf("expected at least 5 schemas, got %d", resp.TotalResults)
	}

	// Index by schema ID
	byID := map[string]SchemaDefinition{}
	for _, s := range resp.Resources {
		byID[s.ID] = s
	}

	wantIDs := []string{
		SchemaUser,
		SchemaGroup,
		SchemaServiceProviderConfig,
		SchemaResourceType,
		SchemaSchema,
	}
	for _, id := range wantIDs {
		if _, ok := byID[id]; !ok {
			t.Errorf("missing schema ID %q", id)
		}
	}

	// User schema must have userName as required attribute
	userSchema := byID[SchemaUser]
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
	groupSchema := byID[SchemaGroup]
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
	endpoints := []struct {
		path    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"/scim/v2/ServiceProviderConfig", ServiceProviderConfig},
		{"/scim/v2/ResourceTypes", ResourceTypes},
		{"/scim/v2/Schemas", Schemas},
	}
	for _, e := range endpoints {
		req := httptest.NewRequest(http.MethodGet, e.path, nil)
		rec := httptest.NewRecorder()
		e.handler(rec, req)
		if ct := rec.Header().Get("Content-Type"); ct != ContentType {
			t.Errorf("%s: expected Content-Type=%q, got %q", e.path, ContentType, ct)
		}
	}
}
