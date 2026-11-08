package handlers

// SCIM filter shims. The grammar lives in internal/scim (extracted per
// docs/HANDLERS_OWNERSHIP.md step 3); these aliases keep the Users/Groups
// endpoints in scim.go and their tests on the names they have always used.
// New code should call internal/scim directly.

import "github.com/FisiFla/freecloud/backend/internal/scim"

// SCIMFilterNode is a node in the parsed filter AST (re-exported from
// internal/scim).
type SCIMFilterNode = scim.FilterNode

const (
	scimOpEq  = scim.OpEq
	scimOpNe  = scim.OpNe
	scimOpCo  = scim.OpCo
	scimOpSw  = scim.OpSw
	scimOpPr  = scim.OpPr
	scimOpAnd = scim.OpAnd
	scimOpOr  = scim.OpOr
	scimOpNot = scim.OpNot
)

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

// parseSCIMFilter returns the flat *scimFilter form used by the list
// handlers, or nil for empty/unparseable input and for logical
// expressions (see scim.ParseFlatFilter).
func parseSCIMFilter(raw string) *scimFilter {
	f := scim.ParseFlatFilter(raw)
	if f == nil {
		return nil
	}
	return &scimFilter{attr: f.Attr, op: f.Op, value: f.Value}
}

// parseSCIMFilterStrict is parseSCIMFilter but returns
// scim.ErrLogicalFilter for logical expressions, so the caller can answer
// 400 invalidFilter instead of quietly dropping the filter.
func parseSCIMFilterStrict(raw string) (*scimFilter, error) {
	f, err := scim.ParseFlatFilterStrict(raw)
	if err != nil || f == nil {
		return nil, err
	}
	return &scimFilter{attr: f.Attr, op: f.Op, value: f.Value}, nil
}
