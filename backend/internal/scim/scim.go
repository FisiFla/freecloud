// Package scim holds the SCIM 2.0 surface: the filter grammar
// (filter.go), the static discovery endpoints (discovery.go), the
// Users/Groups endpoints (users.go, groups.go) with their bearer
// middleware, and the shared schema URNs, Meta block and JSON
// responder. Extracted from internal/handlers per
// docs/HANDLERS_OWNERSHIP.md step 3 (3a: filter+discovery, 3b:
// Users/Groups). The one dependency that stayed behind — the
// onboarding saga write — crosses through the Host seam in
// service.go, implemented on the handlers side by the shims in
// handlers/scim.go; route registration still goes through those
// delegating Handler methods.
package scim

import (
	"encoding/json"
	"net/http"
)

// Schema URNs (RFC 7643) for the resources and messages this server speaks.
const (
	SchemaUser                  = "urn:ietf:params:scim:schemas:core:2.0:User"
	SchemaGroup                 = "urn:ietf:params:scim:schemas:core:2.0:Group"
	SchemaServiceProviderConfig = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	SchemaResourceType          = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
	SchemaSchema                = "urn:ietf:params:scim:schemas:core:2.0:Schema"
	SchemaListResponse          = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
)

// ContentType is the SCIM JSON media type (RFC 7644 §3.1).
const ContentType = "application/scim+json"

// Meta is the SCIM resource metadata block.
type Meta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Version      string `json:"version,omitempty"`
	Location     string `json:"location,omitempty"`
}

// Respond writes v as a SCIM JSON response with the given status code.
func Respond(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
