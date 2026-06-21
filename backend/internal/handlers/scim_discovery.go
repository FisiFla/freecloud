package handlers

// B1: SCIM 2.0 discovery endpoints — RFC 7643 §7 / RFC 7644 §4
//
// These three endpoints are unauthenticated per RFC 7644 §2: a client needs
// them to discover capabilities before it can obtain (or use) a token.
// They are registered OUTSIDE the scimBearerMW group in routes.go.
//
// All responses are static JSON; no DB access is required.

import "net/http"

const (
	scimSPCSchema           = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	scimResourceTypeSchema  = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
	scimSchemaSchema        = "urn:ietf:params:scim:schemas:core:2.0:Schema"
)

// scimSupported is the RFC 7643 §7.3 "supported" flag object.
type scimSupported struct {
	Supported bool `json:"supported"`
}

// scimAuthScheme describes one supported authentication mechanism.
type scimAuthScheme struct {
	Type             string `json:"type"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	SpecURI          string `json:"specUri,omitempty"`
	DocumentationURI string `json:"documentationUri,omitempty"`
	Primary          bool   `json:"primary"`
}

// scimFilterConfig is the filter capability block.
type scimFilterConfig struct {
	Supported  bool `json:"supported"`
	MaxResults int  `json:"maxResults"`
}

// scimSPConfig is the ServiceProviderConfig response body (RFC 7643 §5).
type scimSPConfig struct {
	Schemas               []string         `json:"schemas"`
	DocumentationURI      string           `json:"documentationUri,omitempty"`
	Patch                 scimSupported    `json:"patch"`
	Bulk                  scimBulkConfig   `json:"bulk"`
	Filter                scimFilterConfig `json:"filter"`
	ChangePassword        scimSupported    `json:"changePassword"`
	Sort                  scimSupported    `json:"sort"`
	ETag                  scimSupported    `json:"etag"`
	AuthenticationSchemes []scimAuthScheme `json:"authenticationSchemes"`
	Meta                  scimMeta         `json:"meta"`
}

// scimBulkConfig is the bulk capability block.
type scimBulkConfig struct {
	Supported      bool `json:"supported"`
	MaxOperations  int  `json:"maxOperations"`
	MaxPayloadSize int  `json:"maxPayloadSize"`
}

// SCIMServiceProviderConfig handles GET /scim/v2/ServiceProviderConfig.
// Reports actual server capabilities to allow IdPs to negotiate features.
func (h *Handler) SCIMServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	cfg := scimSPConfig{
		Schemas: []string{scimSPCSchema},
		Patch:   scimSupported{Supported: true},
		Bulk: scimBulkConfig{
			Supported:      false,
			MaxOperations:  0,
			MaxPayloadSize: 0,
		},
		Filter: scimFilterConfig{
			Supported:  true,
			MaxResults: 1000,
		},
		ChangePassword: scimSupported{Supported: false},
		Sort:           scimSupported{Supported: false},
		ETag:           scimSupported{Supported: true},
		AuthenticationSchemes: []scimAuthScheme{
			{
				Type:        "oauthbearertoken",
				Name:        "OAuth Bearer Token",
				Description: "Authentication scheme using the OAuth Bearer Token Standard",
				SpecURI:     "https://www.rfc-editor.org/rfc/rfc6750",
				Primary:     true,
			},
		},
		Meta: scimMeta{
			ResourceType: "ServiceProviderConfig",
			Location:     "/scim/v2/ServiceProviderConfig",
		},
	}
	scimRespond(w, http.StatusOK, cfg)
}

// scimResourceType is a single ResourceType entry (RFC 7643 §6).
type scimResourceType struct {
	Schemas          []string `json:"schemas"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Endpoint         string   `json:"endpoint"`
	Description      string   `json:"description,omitempty"`
	Schema           string   `json:"schema"`
	SchemaExtensions []string `json:"schemaExtensions"`
	Meta             scimMeta `json:"meta"`
}

// scimResourceTypesResponse is the ListResponse for ResourceTypes.
type scimResourceTypesResponse struct {
	Schemas      []string           `json:"schemas"`
	TotalResults int                `json:"totalResults"`
	StartIndex   int                `json:"startIndex"`
	ItemsPerPage int                `json:"itemsPerPage"`
	Resources    []scimResourceType `json:"Resources"`
}

// SCIMResourceTypes handles GET /scim/v2/ResourceTypes.
func (h *Handler) SCIMResourceTypes(w http.ResponseWriter, r *http.Request) {
	types := []scimResourceType{
		{
			Schemas:          []string{scimResourceTypeSchema},
			ID:               "User",
			Name:             "User",
			Endpoint:         "/scim/v2/Users",
			Description:      "User Account",
			Schema:           scimUserSchema,
			SchemaExtensions: []string{},
			Meta: scimMeta{
				ResourceType: "ResourceType",
				Location:     "/scim/v2/ResourceTypes/User",
			},
		},
		{
			Schemas:          []string{scimResourceTypeSchema},
			ID:               "Group",
			Name:             "Group",
			Endpoint:         "/scim/v2/Groups",
			Description:      "Group",
			Schema:           scimGroupSchema,
			SchemaExtensions: []string{},
			Meta: scimMeta{
				ResourceType: "ResourceType",
				Location:     "/scim/v2/ResourceTypes/Group",
			},
		},
	}
	scimRespond(w, http.StatusOK, scimResourceTypesResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: len(types),
		StartIndex:   1,
		ItemsPerPage: len(types),
		Resources:    types,
	})
}

// scimSchemaAttribute describes one attribute in a SCIM schema definition.
type scimSchemaAttribute struct {
	Name            string                `json:"name"`
	Type            string                `json:"type"`
	MultiValued     bool                  `json:"multiValued"`
	Description     string                `json:"description,omitempty"`
	Required        bool                  `json:"required"`
	CaseExact       bool                  `json:"caseExact"`
	Mutability      string                `json:"mutability"`
	Returned        string                `json:"returned"`
	Uniqueness      string                `json:"uniqueness"`
	SubAttributes   []scimSchemaAttribute `json:"subAttributes,omitempty"`
	CanonicalValues []string              `json:"canonicalValues,omitempty"`
	ReferenceTypes  []string              `json:"referenceTypes,omitempty"`
}

// scimSchemaDefinition is one Schema object (RFC 7643 §7).
type scimSchemaDefinition struct {
	Schemas     []string              `json:"schemas"`
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Attributes  []scimSchemaAttribute `json:"attributes"`
	Meta        scimMeta              `json:"meta"`
}

// scimSchemasResponse is the ListResponse for Schemas.
type scimSchemasResponse struct {
	Schemas      []string               `json:"schemas"`
	TotalResults int                    `json:"totalResults"`
	StartIndex   int                    `json:"startIndex"`
	ItemsPerPage int                    `json:"itemsPerPage"`
	Resources    []scimSchemaDefinition `json:"Resources"`
}

// SCIMSchemas handles GET /scim/v2/Schemas.
// Returns User schema, Group schema, and the three meta-schemas.
func (h *Handler) SCIMSchemas(w http.ResponseWriter, r *http.Request) {
	schemas := []scimSchemaDefinition{
		scimUserSchemaDefinition(),
		scimGroupSchemaDefinition(),
		scimSPCSchemaDefinition(),
		scimResourceTypeSchemaDefinition(),
		scimSchemaSchemaDefinition(),
	}
	scimRespond(w, http.StatusOK, scimSchemasResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: len(schemas),
		StartIndex:   1,
		ItemsPerPage: len(schemas),
		Resources:    schemas,
	})
}

// ---- per-schema builders ----

func scimUserSchemaDefinition() scimSchemaDefinition {
	return scimSchemaDefinition{
		Schemas:     []string{scimSchemaSchema},
		ID:          scimUserSchema,
		Name:        "User",
		Description: "User Account (RFC 7643 §4.1)",
		Attributes: []scimSchemaAttribute{
			{
				Name:        "id",
				Type:        "string",
				MultiValued: false,
				Required:    false,
				CaseExact:   true,
				Mutability:  "readOnly",
				Returned:    "always",
				Uniqueness:  "server",
			},
			{
				Name:        "userName",
				Type:        "string",
				MultiValued: false,
				Required:    true,
				CaseExact:   false,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "server",
			},
			{
				Name:        "name",
				Type:        "complex",
				MultiValued: false,
				Required:    false,
				CaseExact:   false,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
				SubAttributes: []scimSchemaAttribute{
					{Name: "formatted", Type: "string", MultiValued: false, Mutability: "readWrite", Returned: "default", Uniqueness: "none"},
					{Name: "familyName", Type: "string", MultiValued: false, Mutability: "readWrite", Returned: "default", Uniqueness: "none"},
					{Name: "givenName", Type: "string", MultiValued: false, Mutability: "readWrite", Returned: "default", Uniqueness: "none"},
				},
			},
			{
				Name:        "emails",
				Type:        "complex",
				MultiValued: true,
				Required:    false,
				CaseExact:   false,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
				SubAttributes: []scimSchemaAttribute{
					{Name: "value", Type: "string", MultiValued: false, Mutability: "readWrite", Returned: "default", Uniqueness: "none"},
					{Name: "type", Type: "string", MultiValued: false, Mutability: "readWrite", Returned: "default", Uniqueness: "none",
						CanonicalValues: []string{"work", "home", "other"}},
					{Name: "primary", Type: "boolean", MultiValued: false, Mutability: "readWrite", Returned: "default", Uniqueness: "none"},
				},
			},
			{
				Name:        "active",
				Type:        "boolean",
				MultiValued: false,
				Required:    false,
				CaseExact:   false,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
			},
			{
				Name:        "meta",
				Type:        "complex",
				MultiValued: false,
				Required:    false,
				CaseExact:   false,
				Mutability:  "readOnly",
				Returned:    "default",
				Uniqueness:  "none",
				SubAttributes: []scimSchemaAttribute{
					{Name: "resourceType", Type: "string", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
					{Name: "created", Type: "dateTime", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
					{Name: "lastModified", Type: "dateTime", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
					{Name: "version", Type: "string", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
					{Name: "location", Type: "reference", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
				},
			},
		},
		Meta: scimMeta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + scimUserSchema,
		},
	}
}

func scimGroupSchemaDefinition() scimSchemaDefinition {
	return scimSchemaDefinition{
		Schemas:     []string{scimSchemaSchema},
		ID:          scimGroupSchema,
		Name:        "Group",
		Description: "Group (RFC 7643 §4.2)",
		Attributes: []scimSchemaAttribute{
			{
				Name:        "id",
				Type:        "string",
				MultiValued: false,
				Required:    false,
				CaseExact:   true,
				Mutability:  "readOnly",
				Returned:    "always",
				Uniqueness:  "server",
			},
			{
				Name:        "displayName",
				Type:        "string",
				MultiValued: false,
				Required:    true,
				CaseExact:   false,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
			},
			{
				Name:        "members",
				Type:        "complex",
				MultiValued: true,
				Required:    false,
				CaseExact:   false,
				Mutability:  "readWrite",
				Returned:    "default",
				Uniqueness:  "none",
				SubAttributes: []scimSchemaAttribute{
					{Name: "value", Type: "string", MultiValued: false, Mutability: "immutable", Returned: "default", Uniqueness: "none"},
					{Name: "display", Type: "string", MultiValued: false, Mutability: "immutable", Returned: "default", Uniqueness: "none"},
					{Name: "$ref", Type: "reference", MultiValued: false, Mutability: "immutable", Returned: "default", Uniqueness: "none",
						ReferenceTypes: []string{"User", "Group"}},
				},
			},
		},
		Meta: scimMeta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + scimGroupSchema,
		},
	}
}

func scimSPCSchemaDefinition() scimSchemaDefinition {
	return scimSchemaDefinition{
		Schemas:     []string{scimSchemaSchema},
		ID:          scimSPCSchema,
		Name:        "ServiceProviderConfig",
		Description: "Schema for representing the service provider's configuration (RFC 7643 §5)",
		Attributes:  []scimSchemaAttribute{},
		Meta: scimMeta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + scimSPCSchema,
		},
	}
}

func scimResourceTypeSchemaDefinition() scimSchemaDefinition {
	return scimSchemaDefinition{
		Schemas:     []string{scimSchemaSchema},
		ID:          scimResourceTypeSchema,
		Name:        "ResourceType",
		Description: "Schema for representing a resource type (RFC 7643 §6)",
		Attributes:  []scimSchemaAttribute{},
		Meta: scimMeta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + scimResourceTypeSchema,
		},
	}
}

func scimSchemaSchemaDefinition() scimSchemaDefinition {
	return scimSchemaDefinition{
		Schemas:     []string{scimSchemaSchema},
		ID:          scimSchemaSchema,
		Name:        "Schema",
		Description: "Schema for representing schemas (RFC 7643 §7)",
		Attributes:  []scimSchemaAttribute{},
		Meta: scimMeta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + scimSchemaSchema,
		},
	}
}
