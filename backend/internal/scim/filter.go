package scim

// B2: SCIM 2.0 filter grammar — RFC 7644 §3.4.2.2
//
// Implements a recursive-descent parser for the SCIM filter language.
// Supported operators: eq, ne, co, sw, pr, and, or, not.
// Supports grouped expressions with ( ) and attribute paths with dots
// (e.g. "emails.value", "name.givenName", "members.value").
//
// The result is a FilterNode that can be evaluated against a flat
// map[string]string of attribute values (e.g. {"username": "alice@..."}).

import (
	"errors"
	"strings"
)

// FilterOp enumerates comparison and logical operators.
type FilterOp int

const (
	OpEq  FilterOp = iota // eq  — equal (case-insensitive for strings)
	OpNe                  // ne  — not equal
	OpCo                  // co  — contains
	OpSw                  // sw  — starts with
	OpPr                  // pr  — present (unary, no value)
	OpAnd                 // and — logical and
	OpOr                  // or  — logical or
	OpNot                 // not — logical not (unary)
)

// FilterNode is a node in the parsed AST.
type FilterNode struct {
	// Leaf fields (op is eq/ne/co/sw/pr)
	Attr  string   // normalised (lowercased) attribute path
	Op    FilterOp // comparison operator
	Value string   // comparison value (empty for pr)

	// Branch fields (op is and/or/not)
	Left  *FilterNode
	Right *FilterNode // nil for not
}

// EvalAttrs is a flat bag of attribute values the evaluator looks up.
// Keys must be lowercase attribute paths.
type EvalAttrs map[string]string

// Eval evaluates the filter node against the provided attribute bag.
// Returns true when the filter matches.
func (n *FilterNode) Eval(attrs EvalAttrs) bool {
	if n == nil {
		return true
	}
	switch n.Op {
	case OpAnd:
		return n.Left.Eval(attrs) && n.Right.Eval(attrs)
	case OpOr:
		return n.Left.Eval(attrs) || n.Right.Eval(attrs)
	case OpNot:
		return !n.Left.Eval(attrs)
	default:
		got := strings.ToLower(attrs[n.Attr])
		want := strings.ToLower(n.Value)
		switch n.Op {
		case OpEq:
			return got == want
		case OpNe:
			return got != want
		case OpCo:
			return strings.Contains(got, want)
		case OpSw:
			return strings.HasPrefix(got, want)
		case OpPr:
			return attrs[n.Attr] != ""
		}
	}
	return false
}

// ParseFilter parses a SCIM filter string into an AST node.
// Returns nil if the input is empty or unparseable (no error surface —
// callers treat nil as "no filter").
func ParseFilter(raw string) *FilterNode {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	p := &filterParser{tokens: tokenize(raw)}
	node := p.parseOr()
	if node == nil || p.pos < len(p.tokens) {
		// Leftover tokens or parse failure — treat as unparseable.
		return nil
	}
	return node
}

// ---- tokenizer ----

// tokenize splits a raw filter string into a flat token slice.
// Quoted strings are kept as single tokens (without the surrounding quotes).
// The format is: each token is a string. We distinguish keywords by value.
func tokenize(raw string) []string {
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

type filterParser struct {
	tokens []string
	pos    int
}

func (p *filterParser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}

func (p *filterParser) consume() string {
	t := p.peek()
	p.pos++
	return t
}

// parseOr handles: expr (or expr)*
func (p *filterParser) parseOr() *FilterNode {
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
		left = &FilterNode{Op: OpOr, Left: left, Right: right}
	}
	return left
}

// parseAnd handles: expr (and expr)*
func (p *filterParser) parseAnd() *FilterNode {
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
		left = &FilterNode{Op: OpAnd, Left: left, Right: right}
	}
	return left
}

// parseUnary handles: not expr | primary
func (p *filterParser) parseUnary() *FilterNode {
	if strings.EqualFold(p.peek(), "not") {
		p.consume()
		child := p.parsePrimary()
		if child == nil {
			return nil
		}
		return &FilterNode{Op: OpNot, Left: child}
	}
	return p.parsePrimary()
}

// parsePrimary handles: ( expr ) | attrPath op value | attrPath pr
func (p *filterParser) parsePrimary() *FilterNode {
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
		innerParser := &filterParser{tokens: tokenize(inner)}
		return innerParser.parseOr()
	}

	op := strings.ToLower(p.peek())
	if op == "" {
		return nil
	}

	// "pr" is a unary presence test — no value follows
	if op == "pr" {
		p.consume()
		return &FilterNode{Attr: attr, Op: OpPr}
	}

	var filterOp FilterOp
	switch op {
	case "eq":
		filterOp = OpEq
	case "ne":
		filterOp = OpNe
	case "co":
		filterOp = OpCo
	case "sw":
		filterOp = OpSw
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

	return &FilterNode{Attr: attr, Op: filterOp, Value: value}
}

// ---- flat filter ----
//
// FlatFilter is the simple single-comparison filter representation the
// Users/Groups list handlers build SQL predicates from. New code that needs
// the full grammar should use ParseFilter + FilterNode directly.
type FlatFilter struct {
	Attr  string // normalised (lowercased) attribute path
	Op    string // operator keyword: "eq", "ne", "co", "sw" or "pr"
	Value string // comparison value (empty for pr)
}

// ParseFlatFilter wraps ParseFilter and returns the flat *FlatFilter form.
//
// A logical expression (and/or/not) is NOT representable in the flat
// FlatFilter type. ParseFlatFilter therefore returns nil for it, which the
// callers used to read as "no filter" — so a client sending
// `userName eq "a" and userName eq "b"` silently got the whole user list
// back instead of the 400 invalidFilter RFC 7644 §3.4.2.2 requires. Use
// ParseFlatFilterStrict where the difference matters.
func ParseFlatFilter(raw string) *FlatFilter {
	node := ParseFilter(raw)
	if node == nil {
		return nil
	}
	// Only leaf comparisons map to the flat struct; logical nodes are not
	// representable — return nil so callers fall back to no-filter behaviour.
	switch node.Op {
	case OpEq, OpNe, OpCo, OpSw, OpPr:
		return &FlatFilter{
			Attr:  node.Attr,
			Op:    node.Op.String(),
			Value: node.Value,
		}
	}
	return nil
}

// ErrLogicalFilter marks a filter that parses into a logical expression
// (and/or/not). The list handlers cannot evaluate those, and silently
// ignoring them would return every row instead of the filtered set.
var ErrLogicalFilter = errors.New("unsupported logical SCIM filter")

// ParseFlatFilterStrict parses raw into the flat comparison filter the list
// handlers understand.
//
// It returns ErrLogicalFilter when the filter is a logical expression,
// so the caller can answer 400 invalidFilter instead of quietly dropping the
// filter. An empty or unparseable filter returns (nil, nil) — "no filter" —
// which keeps the previous behaviour for clients sending a malformed filter.
func ParseFlatFilterStrict(raw string) (*FlatFilter, error) {
	node := ParseFilter(raw)
	if node == nil {
		return nil, nil
	}
	switch node.Op {
	case OpEq, OpNe, OpCo, OpSw, OpPr:
		return &FlatFilter{
			Attr:  node.Attr,
			Op:    node.Op.String(),
			Value: node.Value,
		}, nil
	}
	return nil, ErrLogicalFilter
}

// String returns the SCIM operator keyword ("eq", "ne", ...) for
// leaf comparison operators, or "" for logical operators.
func (op FilterOp) String() string {
	switch op {
	case OpEq:
		return "eq"
	case OpNe:
		return "ne"
	case OpCo:
		return "co"
	case OpSw:
		return "sw"
	case OpPr:
		return "pr"
	}
	return ""
}

// ---- filter-qualified path parser ----
//
// ParseFilterPath parses a path like "members[value eq \"user-id\"]" into
// its attribute base ("members") and the inner filter node.  Returns ("", nil)
// on failure.

func ParseFilterPath(path string) (base string, inner *FilterNode) {
	open := strings.Index(path, "[")
	close := strings.LastIndex(path, "]")
	if open == -1 || close == -1 || close < open {
		return "", nil
	}
	base = strings.ToLower(strings.TrimSpace(path[:open]))
	innerStr := path[open+1 : close]
	inner = ParseFilter(innerStr)
	return base, inner
}
