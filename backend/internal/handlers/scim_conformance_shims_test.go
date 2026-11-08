//go:build integration

package handlers

// Type aliases used only by the integration-tagged SCIM conformance
// suite (scim_conformance_test.go). They live here, under the same
// build tag, rather than with the production shims in scim.go so that
// plain staticcheck (no tags, as CI runs it) does not report them as
// unused (U1000).

import "github.com/FisiFla/freecloud/backend/internal/scim"

type scimEmail = scim.Email
type scimListResponse = scim.UserListResponse
