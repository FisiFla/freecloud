package handlers

// SCIM filter shims. The grammar lives in internal/scim (extracted per
// docs/HANDLERS_OWNERSHIP.md step 3); these aliases keep the handler
// tests on the names they have always used. The endpoints themselves
// moved to internal/scim in step 3b and call the grammar directly.
// New code should call internal/scim directly.

import "github.com/FisiFla/freecloud/backend/internal/scim"

// SCIMFilterNode is a node in the parsed filter AST (re-exported from
// internal/scim).
type SCIMFilterNode = scim.FilterNode

// ParseSCIMFilter parses a SCIM filter string into an AST node. Returns
// nil if the input is empty or unparseable.
func ParseSCIMFilter(raw string) *SCIMFilterNode {
	return scim.ParseFilter(raw)
}

// ParseSCIMFilterPath parses a path like "members[value eq \"user-id\"]"
// into its attribute base ("members") and the inner filter node.
func ParseSCIMFilterPath(path string) (base string, inner *SCIMFilterNode) {
	return scim.ParseFilterPath(path)
}

// scimFilter is the simple flat filter representation the list handlers
// used before the move to internal/scim, kept for the handler tests
// that still assert on parseSCIMFilter's output.
type scimFilter struct {
	attr  string
	op    string
	value string
}

// parseSCIMFilter returns the flat *scimFilter form, or nil for
// empty/unparseable input and for logical expressions (see
// scim.ParseFlatFilter).
func parseSCIMFilter(raw string) *scimFilter {
	f := scim.ParseFlatFilter(raw)
	if f == nil {
		return nil
	}
	return &scimFilter{attr: f.Attr, op: f.Op, value: f.Value}
}
