// Package scim holds the dependency-free core of the SCIM 2.0 surface:
// the filter grammar (filter.go), the static discovery endpoints
// (discovery.go), and the shared schema URNs, Meta block and JSON
// responder. Extracted from internal/handlers per
// docs/HANDLERS_OWNERSHIP.md step 3; the Users/Groups endpoints remain
// in handlers (they need the persistOnboard seam) and reach this
// package through the shims in handlers/scim_filter.go and
// handlers/scim_discovery.go.
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
