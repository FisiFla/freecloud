package handlers

// SCIM discovery shims. The endpoints live in internal/scim (extracted
// per docs/HANDLERS_OWNERSHIP.md step 3) as plain handler funcs — they
// are static JSON and need no Handler state. These methods keep
// routes.go and the handler tests unchanged; they stay registered
// OUTSIDE the scimBearerMW group (RFC 7644 §2).

import (
	"net/http"

	"github.com/FisiFla/freecloud/backend/internal/scim"
)

const (
	scimSPCSchema          = scim.SchemaServiceProviderConfig
	scimResourceTypeSchema = scim.SchemaResourceType
	scimSchemaSchema       = scim.SchemaSchema
)

// scimSPConfig is the ServiceProviderConfig response body (re-exported
// from internal/scim for the conformance tests).
type scimSPConfig = scim.SPConfig

// SCIMServiceProviderConfig handles GET /scim/v2/ServiceProviderConfig.
func (h *Handler) SCIMServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	scim.ServiceProviderConfig(w, r)
}

// SCIMResourceTypes handles GET /scim/v2/ResourceTypes.
func (h *Handler) SCIMResourceTypes(w http.ResponseWriter, r *http.Request) {
	scim.ResourceTypes(w, r)
}

// SCIMSchemas handles GET /scim/v2/Schemas.
func (h *Handler) SCIMSchemas(w http.ResponseWriter, r *http.Request) {
	scim.Schemas(w, r)
}
