package scim

// B1: SCIM 2.0 discovery endpoints — RFC 7643 §7 / RFC 7644 §4
//
// These three endpoints are unauthenticated per RFC 7644 §2: a client needs
// them to discover capabilities before it can obtain (or use) a token.
// They are registered OUTSIDE the scimBearerMW group in routes.go.
//
// All responses are static JSON; no DB access is required.

import "net/http"

// Supported is the RFC 7643 §7.3 "supported" flag object.
type Supported struct {
	Supported bool `json:"supported"`
}

// AuthScheme describes one supported authentication mechanism.
type AuthScheme struct {
	Type             string `json:"type"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	SpecURI          string `json:"specUri,omitempty"`
	DocumentationURI string `json:"documentationUri,omitempty"`
	Primary          bool   `json:"primary"`
}

// FilterConfig is the filter capability block.
type FilterConfig struct {
	Supported  bool `json:"supported"`
	MaxResults int  `json:"maxResults"`
}

// SPConfig is the ServiceProviderConfig response body (RFC 7643 §5).
type SPConfig struct {
	Schemas               []string     `json:"schemas"`
	DocumentationURI      string       `json:"documentationUri,omitempty"`
	Patch                 Supported    `json:"patch"`
	Bulk                  BulkConfig   `json:"bulk"`
	Filter                FilterConfig `json:"filter"`
	ChangePassword        Supported    `json:"changePassword"`
	Sort                  Supported    `json:"sort"`
	ETag                  Supported    `json:"etag"`
	AuthenticationSchemes []AuthScheme `json:"authenticationSchemes"`
	Meta                  Meta         `json:"meta"`
}

// BulkConfig is the bulk capability block.
type BulkConfig struct {
	Supported      bool `json:"supported"`
	MaxOperations  int  `json:"maxOperations"`
	MaxPayloadSize int  `json:"maxPayloadSize"`
}

// ServiceProviderConfig handles GET /scim/v2/ServiceProviderConfig.
// Reports actual server capabilities to allow IdPs to negotiate features.
func ServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	cfg := SPConfig{
		Schemas: []string{SchemaServiceProviderConfig},
		Patch:   Supported{Supported: true},
		Bulk: BulkConfig{
			Supported:      false,
			MaxOperations:  0,
			MaxPayloadSize: 0,
		},
		Filter: FilterConfig{
			Supported:  true,
			MaxResults: 1000,
		},
		ChangePassword: Supported{Supported: false},
		Sort:           Supported{Supported: false},
		ETag:           Supported{Supported: true},
		AuthenticationSchemes: []AuthScheme{
			{
				Type:        "oauthbearertoken",
				Name:        "OAuth Bearer Token",
				Description: "Authentication scheme using the OAuth Bearer Token Standard",
				SpecURI:     "https://www.rfc-editor.org/rfc/rfc6750",
				Primary:     true,
			},
		},
		Meta: Meta{
			ResourceType: "ServiceProviderConfig",
			Location:     "/scim/v2/ServiceProviderConfig",
		},
	}
	Respond(w, http.StatusOK, cfg)
}

// ResourceType is a single ResourceType entry (RFC 7643 §6).
type ResourceType struct {
	Schemas          []string `json:"schemas"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Endpoint         string   `json:"endpoint"`
	Description      string   `json:"description,omitempty"`
	Schema           string   `json:"schema"`
	SchemaExtensions []string `json:"schemaExtensions"`
	Meta             Meta     `json:"meta"`
}

// ResourceTypesResponse is the ListResponse for ResourceTypes.
type ResourceTypesResponse struct {
	Schemas      []string       `json:"schemas"`
	TotalResults int            `json:"totalResults"`
	StartIndex   int            `json:"startIndex"`
	ItemsPerPage int            `json:"itemsPerPage"`
	Resources    []ResourceType `json:"Resources"`
}

// ResourceTypes handles GET /scim/v2/ResourceTypes.
func ResourceTypes(w http.ResponseWriter, r *http.Request) {
	types := []ResourceType{
		{
			Schemas:          []string{SchemaResourceType},
			ID:               "User",
			Name:             "User",
			Endpoint:         "/scim/v2/Users",
			Description:      "User Account",
			Schema:           SchemaUser,
			SchemaExtensions: []string{},
			Meta: Meta{
				ResourceType: "ResourceType",
				Location:     "/scim/v2/ResourceTypes/User",
			},
		},
		{
			Schemas:          []string{SchemaResourceType},
			ID:               "Group",
			Name:             "Group",
			Endpoint:         "/scim/v2/Groups",
			Description:      "Group",
			Schema:           SchemaGroup,
			SchemaExtensions: []string{},
			Meta: Meta{
				ResourceType: "ResourceType",
				Location:     "/scim/v2/ResourceTypes/Group",
			},
		},
	}
	Respond(w, http.StatusOK, ResourceTypesResponse{
		Schemas:      []string{SchemaListResponse},
		TotalResults: len(types),
		StartIndex:   1,
		ItemsPerPage: len(types),
		Resources:    types,
	})
}

// SchemaAttribute describes one attribute in a SCIM schema definition.
type SchemaAttribute struct {
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	MultiValued     bool              `json:"multiValued"`
	Description     string            `json:"description,omitempty"`
	Required        bool              `json:"required"`
	CaseExact       bool              `json:"caseExact"`
	Mutability      string            `json:"mutability"`
	Returned        string            `json:"returned"`
	Uniqueness      string            `json:"uniqueness"`
	SubAttributes   []SchemaAttribute `json:"subAttributes,omitempty"`
	CanonicalValues []string          `json:"canonicalValues,omitempty"`
	ReferenceTypes  []string          `json:"referenceTypes,omitempty"`
}

// SchemaDefinition is one Schema object (RFC 7643 §7).
type SchemaDefinition struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Attributes  []SchemaAttribute `json:"attributes"`
	Meta        Meta              `json:"meta"`
}

// SchemasResponse is the ListResponse for Schemas.
type SchemasResponse struct {
	Schemas      []string           `json:"schemas"`
	TotalResults int                `json:"totalResults"`
	StartIndex   int                `json:"startIndex"`
	ItemsPerPage int                `json:"itemsPerPage"`
	Resources    []SchemaDefinition `json:"Resources"`
}

// Schemas handles GET /scim/v2/Schemas.
// Returns User schema, Group schema, and the three meta-schemas.
func Schemas(w http.ResponseWriter, r *http.Request) {
	schemas := []SchemaDefinition{
		userSchemaDefinition(),
		groupSchemaDefinition(),
		spcSchemaDefinition(),
		resourceTypeSchemaDefinition(),
		schemaSchemaDefinition(),
	}
	Respond(w, http.StatusOK, SchemasResponse{
		Schemas:      []string{SchemaListResponse},
		TotalResults: len(schemas),
		StartIndex:   1,
		ItemsPerPage: len(schemas),
		Resources:    schemas,
	})
}

// ---- per-schema builders ----

func userSchemaDefinition() SchemaDefinition {
	return SchemaDefinition{
		Schemas:     []string{SchemaSchema},
		ID:          SchemaUser,
		Name:        "User",
		Description: "User Account (RFC 7643 §4.1)",
		Attributes: []SchemaAttribute{
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
				SubAttributes: []SchemaAttribute{
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
				SubAttributes: []SchemaAttribute{
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
				SubAttributes: []SchemaAttribute{
					{Name: "resourceType", Type: "string", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
					{Name: "created", Type: "dateTime", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
					{Name: "lastModified", Type: "dateTime", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
					{Name: "version", Type: "string", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
					{Name: "location", Type: "reference", MultiValued: false, Mutability: "readOnly", Returned: "default", Uniqueness: "none"},
				},
			},
		},
		Meta: Meta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + SchemaUser,
		},
	}
}

func groupSchemaDefinition() SchemaDefinition {
	return SchemaDefinition{
		Schemas:     []string{SchemaSchema},
		ID:          SchemaGroup,
		Name:        "Group",
		Description: "Group (RFC 7643 §4.2)",
		Attributes: []SchemaAttribute{
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
				SubAttributes: []SchemaAttribute{
					{Name: "value", Type: "string", MultiValued: false, Mutability: "immutable", Returned: "default", Uniqueness: "none"},
					{Name: "display", Type: "string", MultiValued: false, Mutability: "immutable", Returned: "default", Uniqueness: "none"},
					{Name: "$ref", Type: "reference", MultiValued: false, Mutability: "immutable", Returned: "default", Uniqueness: "none",
						ReferenceTypes: []string{"User", "Group"}},
				},
			},
		},
		Meta: Meta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + SchemaGroup,
		},
	}
}

func spcSchemaDefinition() SchemaDefinition {
	return SchemaDefinition{
		Schemas:     []string{SchemaSchema},
		ID:          SchemaServiceProviderConfig,
		Name:        "ServiceProviderConfig",
		Description: "Schema for representing the service provider's configuration (RFC 7643 §5)",
		Attributes:  []SchemaAttribute{},
		Meta: Meta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + SchemaServiceProviderConfig,
		},
	}
}

func resourceTypeSchemaDefinition() SchemaDefinition {
	return SchemaDefinition{
		Schemas:     []string{SchemaSchema},
		ID:          SchemaResourceType,
		Name:        "ResourceType",
		Description: "Schema for representing a resource type (RFC 7643 §6)",
		Attributes:  []SchemaAttribute{},
		Meta: Meta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + SchemaResourceType,
		},
	}
}

func schemaSchemaDefinition() SchemaDefinition {
	return SchemaDefinition{
		Schemas:     []string{SchemaSchema},
		ID:          SchemaSchema,
		Name:        "Schema",
		Description: "Schema for representing schemas (RFC 7643 §7)",
		Attributes:  []SchemaAttribute{},
		Meta: Meta{
			ResourceType: "Schema",
			Location:     "/scim/v2/Schemas/" + SchemaSchema,
		},
	}
}
