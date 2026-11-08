package scim

// SCIM JSON resource types for the Users/Groups endpoints (RFC 7643).

// ---- SCIM JSON types ----

// Name is the SCIM User name sub-attribute.
type Name struct {
	Formatted  string `json:"formatted,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
}

// Email is a SCIM multi-value email entry.
type Email struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

// User is the SCIM User resource representation (RFC 7643 §4.1).
type User struct {
	Schemas    []string `json:"schemas"`
	ID         string   `json:"id,omitempty"`
	ExternalID string   `json:"externalId,omitempty"`
	UserName   string   `json:"userName"`
	Name       Name     `json:"name,omitempty"`
	Emails     []Email  `json:"emails,omitempty"`
	Active     bool     `json:"active"`
	Meta       Meta     `json:"meta,omitempty"`
}

// UserListResponse is the SCIM ListResponse envelope.
type UserListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []User   `json:"Resources"`
}

// ErrorResponse is the SCIM error response (RFC 7644 §3.12).
type ErrorResponse struct {
	Schemas  []string `json:"schemas"`
	Status   string   `json:"status"`
	Detail   string   `json:"detail,omitempty"`
	ScimType string   `json:"scimType,omitempty"`
}

// PatchOp is a single PATCH operation.
type PatchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path,omitempty"`
	Value interface{} `json:"value"`
}

// PatchRequest is the SCIM PATCH body.
type PatchRequest struct {
	Schemas    []string  `json:"schemas"`
	Operations []PatchOp `json:"Operations"`
}

const (
	SchemaPatchOp = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	SchemaError   = "urn:ietf:params:scim:api:messages:2.0:Error"
)
