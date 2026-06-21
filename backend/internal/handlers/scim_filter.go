package handlers

// B2: SCIM 2.0 filter grammar — RFC 7644 §3.4.2.2
//
// Implements a recursive-descent parser for the SCIM filter language.
// Supported operators: eq, ne, co, sw, pr, and, or, not.
// Supports grouped expressions with ( ) and attribute paths with dots
// (e.g. "emails.value", "name.givenName", "members.value").
//
// The result is a SCIMFilterNode that can be evaluated against a flat
// map[string]string of attribute values (e.g. {"username": "alice@..."}).

import (
	"strings"
)

// scimFilterOp enumerates comparison and logical operators.
type scimFilterOp int

const (
	scimOpEq  scimFilterOp = iota // eq  — equal (case-insensitive for strings)
	scimOpNe                      // ne  — not equal
	scimOpCo                      // co  — contains
	scimOpSw                      // sw  — starts with
	scimOpPr                      // pr  — present (unary, no value)
	scimOpAnd                     // and — logical and
	scimOpOr                      // or  — logical or
	scimOpNot                     // not — logical not (unary)
)

// SCIMFilterNode is a node in the parsed AST.
type SCIMFilterNode struct {
	// Leaf fields (op is eq/ne/co/sw/pr)
	Attr  string       // normalised (lowercased) attribute path
	Op    scimFilterOp // comparison operator
	Value string       // comparison value (empty for pr)

	// Branch fields (op is and/or/not)
	Left  *SCIMFilterNode
	Right *SCIMFilterNode // nil for not
}

// scimFilterEvalAttrs is a flat bag of attribute values the evaluator looks up.
// Keys must be lowercase attribute paths.
type scimFilterEvalAttrs map[string]string

// Eval evaluates the filter node against the provided attribute bag.
// Returns true when the filter matches.
func (n *SCIMFilterNode) Eval(attrs scimFilterEvalAttrs) bool {
	if n == nil {
		return true
	}
	switch n.Op {
	case scimOpAnd:
		return n.Left.Eval(attrs) && n.Right.Eval(attrs)
	case scimOpOr:
		return n.Left.Eval(attrs) || n.Right.Eval(attrs)
	case scimOpNot:
		return !n.Left.Eval(attrs)
	default:
		got := strings.ToLower(attrs[n.Attr])
		want := strings.ToLower(n.Value)
		switch n.Op {
		case scimOpEq:
			return got == want
		case scimOpNe:
			return got != want
		case scimOpCo:
			return strings.Contains(got, want)
		case scimOpSw:
			return strings.HasPrefix(got, want)
		case scimOpPr:
			return attrs[n.Attr] != ""
		}
	}
	return false
}

// ParseSCIMFilter parses a SCIM filter string into an AST node.
// Returns nil if the input is empty or unparseable (no error surface —
// callers treat nil as "no filter").
func ParseSCIMFilter(raw string) *SCIMFilterNode {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	p := &scimFilterParser{tokens: scimTokenize(raw)}
	node := p.parseOr()
	if node == nil || p.pos < len(p.tokens) {
		// Leftover tokens or parse failure — treat as unparseable.
		return nil
	}
	return node
}

// ---- tokenizer ----

// scimTokenize splits a raw filter string into a flat token slice.
// Quoted strings are kept as single tokens (without the surrounding quotes).
// The format is: each token is a string. We distinguish keywords by value.
func scimTokenize(raw string) []string {
	var tokens []string
	i := 0
	for i < len(raw) {
		c := raw[i]
		// Skip whitespace
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		// Parentheses as individual tokens
		if c == '(' || c == ')' {
			tokens = append(tokens, string(c))
			i++
			continue
		}
		// Quoted string
		if c == '"' || c == '\'' {
			j := i + 1
			for j < len(raw) && raw[j] != c {
				j++
			}
			tokens = append(tokens, raw[i+1:j])
			if j < len(raw) {
				j++ // skip closing quote
			}
			i = j
			continue
		}
		// Word (attribute, operator, keyword)
		j := i
		for j < len(raw) && raw[j] != ' ' && raw[j] != '\t' && raw[j] != '(' && raw[j] != ')' {
			j++
		}
		if j > i {
			tokens = append(tokens, raw[i:j])
		}
		i = j
	}
	return tokens
}

// ---- recursive-descent parser ----

type scimFilterParser struct {
	tokens []string
	pos    int
}

func (p *scimFilterParser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}

func (p *scimFilterParser) consume() string {
	t := p.peek()
	p.pos++
	return t
}

// parseOr handles: expr (or expr)*
func (p *scimFilterParser) parseOr() *SCIMFilterNode {
	left := p.parseAnd()
	if left == nil {
		return nil
	}
	for strings.EqualFold(p.peek(), "or") {
		p.consume()
		right := p.parseAnd()
		if right == nil {
			return nil
		}
		left = &SCIMFilterNode{Op: scimOpOr, Left: left, Right: right}
	}
	return left
}

// parseAnd handles: expr (and expr)*
func (p *scimFilterParser) parseAnd() *SCIMFilterNode {
	left := p.parseUnary()
	if left == nil {
		return nil
	}
	for strings.EqualFold(p.peek(), "and") {
		p.consume()
		right := p.parseUnary()
		if right == nil {
			return nil
		}
		left = &SCIMFilterNode{Op: scimOpAnd, Left: left, Right: right}
	}
	return left
}

// parseUnary handles: not expr | primary
func (p *scimFilterParser) parseUnary() *SCIMFilterNode {
	if strings.EqualFold(p.peek(), "not") {
		p.consume()
		child := p.parsePrimary()
		if child == nil {
			return nil
		}
		return &SCIMFilterNode{Op: scimOpNot, Left: child}
	}
	return p.parsePrimary()
}

// parsePrimary handles: ( expr ) | attrPath op value | attrPath pr
func (p *scimFilterParser) parsePrimary() *SCIMFilterNode {
	if p.peek() == "(" {
		p.consume()
		node := p.parseOr()
		if p.peek() == ")" {
			p.consume()
		}
		return node
	}

	// attrPath
	attr := p.consume()
	if attr == "" {
		return nil
	}
	attr = strings.ToLower(attr)

	// Handle filter-qualified path like "members[value eq "uid"]"
	// Decompose into the base path and strip the bracket part (return leaf for the inner).
	if idx := strings.Index(attr, "["); idx != -1 {
		// attr is something like "members[value" — the bracket segment spans multiple tokens.
		// Re-assemble tokens until we see "]".
		inner := attr[idx+1:] // partial first token after "["
		for p.peek() != "]" && p.peek() != "" {
			inner += " " + p.consume()
		}
		if p.peek() == "]" {
			p.consume()
		}
		// Parse the inner expression and return it (the base path context is
		// used by the caller to know which resource attribute to look up).
		innerParser := &scimFilterParser{tokens: scimTokenize(inner)}
		return innerParser.parseOr()
	}

	op := strings.ToLower(p.peek())
	if op == "" {
		return nil
	}

	// "pr" is a unary presence test — no value follows
	if op == "pr" {
		p.consume()
		return &SCIMFilterNode{Attr: attr, Op: scimOpPr}
	}

	var filterOp scimFilterOp
	switch op {
	case "eq":
		filterOp = scimOpEq
	case "ne":
		filterOp = scimOpNe
	case "co":
		filterOp = scimOpCo
	case "sw":
		filterOp = scimOpSw
	default:
		// Unknown operator — unrecognised filter, return nil.
		return nil
	}
	p.consume() // consume operator token

	value := p.consume()
	if value == "" {
		return nil
	}
	// Strip remaining quotes just in case the tokenizer left them.
	value = strings.Trim(value, "\"'")

	return &SCIMFilterNode{Attr: attr, Op: filterOp, Value: value}
}

// ---- backward-compat shim ----
//
// parseSCIMFilter wraps ParseSCIMFilter and returns the old *scimFilter type
// so the existing Users/Groups list handlers continue to compile unchanged.
// New code should use ParseSCIMFilter + SCIMFilterNode directly.

func parseSCIMFilter(raw string) *scimFilter {
	node := ParseSCIMFilter(raw)
	if node == nil {
		return nil
	}
	// Only leaf comparisons map to the old struct; logical nodes are not supported
	// by the old type — return nil so callers fall back to no-filter behaviour.
	switch node.Op {
	case scimOpEq, scimOpNe, scimOpCo, scimOpSw, scimOpPr:
		return &scimFilter{
			attr:  node.Attr,
			op:    scimFilterOpString(node.Op),
			value: node.Value,
		}
	}
	return nil
}

func scimFilterOpString(op scimFilterOp) string {
	switch op {
	case scimOpEq:
		return "eq"
	case scimOpNe:
		return "ne"
	case scimOpCo:
		return "co"
	case scimOpSw:
		return "sw"
	case scimOpPr:
		return "pr"
	}
	return ""
}

// ---- filter-qualified path parser ----
//
// ParseSCIMFilterPath parses a path like "members[value eq \"user-id\"]" into
// its attribute base ("members") and the inner filter node.  Returns ("", nil)
// on failure.

func ParseSCIMFilterPath(path string) (base string, inner *SCIMFilterNode) {
	open := strings.Index(path, "[")
	close := strings.LastIndex(path, "]")
	if open == -1 || close == -1 || close < open {
		return "", nil
	}
	base = strings.ToLower(strings.TrimSpace(path[:open]))
	innerStr := path[open+1 : close]
	inner = ParseSCIMFilter(innerStr)
	return base, inner
}
