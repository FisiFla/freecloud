package scim

// Tests for B2: SCIM filter parser (filter.go).

import (
	"testing"
)

// ---- ParseFilter ----

func TestParseSCIMFilter_Eq(t *testing.T) {
	node := ParseFilter(`userName eq "alice@example.com"`)
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Op != OpEq {
		t.Errorf("expected op eq, got %v", node.Op)
	}
	if node.Attr != "username" {
		t.Errorf("expected attr=username, got %q", node.Attr)
	}
	if node.Value != "alice@example.com" {
		t.Errorf("expected value=alice@example.com, got %q", node.Value)
	}
}

func TestParseSCIMFilter_Co(t *testing.T) {
	node := ParseFilter(`userName co "ali"`)
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Op != OpCo {
		t.Errorf("expected op co, got %v", node.Op)
	}
	if node.Value != "ali" {
		t.Errorf("expected value=ali, got %q", node.Value)
	}
}

func TestParseSCIMFilter_Sw(t *testing.T) {
	node := ParseFilter(`emails.value sw "alice"`)
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Op != OpSw {
		t.Errorf("expected op sw, got %v", node.Op)
	}
	if node.Attr != "emails.value" {
		t.Errorf("expected attr=emails.value, got %q", node.Attr)
	}
}

func TestParseSCIMFilter_Pr(t *testing.T) {
	node := ParseFilter(`emails pr`)
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Op != OpPr {
		t.Errorf("expected op pr, got %v", node.Op)
	}
	if node.Attr != "emails" {
		t.Errorf("expected attr=emails, got %q", node.Attr)
	}
}

func TestParseSCIMFilter_And(t *testing.T) {
	node := ParseFilter(`userName eq "alice" and active eq "true"`)
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Op != OpAnd {
		t.Errorf("expected op and, got %v", node.Op)
	}
	if node.Left == nil || node.Right == nil {
		t.Fatal("expected left and right children")
	}
	if node.Left.Attr != "username" {
		t.Errorf("left attr: expected username, got %q", node.Left.Attr)
	}
	if node.Right.Attr != "active" {
		t.Errorf("right attr: expected active, got %q", node.Right.Attr)
	}
}

func TestParseSCIMFilter_Or(t *testing.T) {
	node := ParseFilter(`userName eq "alice" or userName eq "bob"`)
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Op != OpOr {
		t.Errorf("expected op or, got %v", node.Op)
	}
}

func TestParseSCIMFilter_Not(t *testing.T) {
	node := ParseFilter(`not (active eq "true")`)
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Op != OpNot {
		t.Errorf("expected op not, got %v", node.Op)
	}
	if node.Left == nil {
		t.Fatal("expected left child for not")
	}
}

func TestParseSCIMFilter_EmptyOrInvalid(t *testing.T) {
	if ParseFilter("") != nil {
		t.Error("empty string: expected nil")
	}
	if ParseFilter("bad") != nil {
		t.Error("single token: expected nil")
	}
}

func TestParseSCIMFilter_CaseInsensitiveOp(t *testing.T) {
	node := ParseFilter(`userName EQ "alice"`)
	if node == nil {
		t.Fatal("expected non-nil node for uppercase EQ")
	}
	if node.Op != OpEq {
		t.Errorf("expected op eq, got %v", node.Op)
	}
}

// ---- FilterNode.Eval ----

func TestSCIMFilterNode_Eval_Eq(t *testing.T) {
	node := &FilterNode{Attr: "username", Op: OpEq, Value: "alice"}
	if !node.Eval(EvalAttrs{"username": "alice"}) {
		t.Error("expected match")
	}
	if node.Eval(EvalAttrs{"username": "bob"}) {
		t.Error("expected no match")
	}
}

func TestSCIMFilterNode_Eval_EqCaseInsensitive(t *testing.T) {
	node := &FilterNode{Attr: "username", Op: OpEq, Value: "Alice"}
	if !node.Eval(EvalAttrs{"username": "alice"}) {
		t.Error("eq should be case-insensitive")
	}
}

func TestSCIMFilterNode_Eval_Ne(t *testing.T) {
	node := &FilterNode{Attr: "active", Op: OpNe, Value: "true"}
	if !node.Eval(EvalAttrs{"active": "false"}) {
		t.Error("expected ne match")
	}
	if node.Eval(EvalAttrs{"active": "true"}) {
		t.Error("expected ne no-match")
	}
}

func TestSCIMFilterNode_Eval_Co(t *testing.T) {
	node := &FilterNode{Attr: "username", Op: OpCo, Value: "ali"}
	if !node.Eval(EvalAttrs{"username": "alice@example.com"}) {
		t.Error("expected co match")
	}
	if node.Eval(EvalAttrs{"username": "bob@example.com"}) {
		t.Error("expected co no-match")
	}
}

func TestSCIMFilterNode_Eval_Sw(t *testing.T) {
	node := &FilterNode{Attr: "username", Op: OpSw, Value: "ali"}
	if !node.Eval(EvalAttrs{"username": "alice@example.com"}) {
		t.Error("expected sw match")
	}
	if node.Eval(EvalAttrs{"username": "xalice@example.com"}) {
		t.Error("expected sw no-match on non-prefix")
	}
}

func TestSCIMFilterNode_Eval_Pr(t *testing.T) {
	node := &FilterNode{Attr: "emails", Op: OpPr}
	if !node.Eval(EvalAttrs{"emails": "alice@example.com"}) {
		t.Error("expected pr match when attr is set")
	}
	if node.Eval(EvalAttrs{}) {
		t.Error("expected pr no-match when attr is absent")
	}
}

func TestSCIMFilterNode_Eval_And(t *testing.T) {
	left := &FilterNode{Attr: "username", Op: OpEq, Value: "alice"}
	right := &FilterNode{Attr: "active", Op: OpEq, Value: "true"}
	and := &FilterNode{Op: OpAnd, Left: left, Right: right}

	if !and.Eval(EvalAttrs{"username": "alice", "active": "true"}) {
		t.Error("expected and match")
	}
	if and.Eval(EvalAttrs{"username": "alice", "active": "false"}) {
		t.Error("expected and no-match when one side fails")
	}
}

func TestSCIMFilterNode_Eval_Or(t *testing.T) {
	left := &FilterNode{Attr: "username", Op: OpEq, Value: "alice"}
	right := &FilterNode{Attr: "username", Op: OpEq, Value: "bob"}
	or := &FilterNode{Op: OpOr, Left: left, Right: right}

	if !or.Eval(EvalAttrs{"username": "alice"}) {
		t.Error("expected or match for alice")
	}
	if !or.Eval(EvalAttrs{"username": "bob"}) {
		t.Error("expected or match for bob")
	}
	if or.Eval(EvalAttrs{"username": "charlie"}) {
		t.Error("expected or no-match for charlie")
	}
}

func TestSCIMFilterNode_Eval_Not(t *testing.T) {
	inner := &FilterNode{Attr: "active", Op: OpEq, Value: "true"}
	not := &FilterNode{Op: OpNot, Left: inner}

	if !not.Eval(EvalAttrs{"active": "false"}) {
		t.Error("expected not match when inner is false")
	}
	if not.Eval(EvalAttrs{"active": "true"}) {
		t.Error("expected not no-match when inner is true")
	}
}

func TestSCIMFilterNode_Eval_Nil(t *testing.T) {
	var n *FilterNode
	if !n.Eval(EvalAttrs{}) {
		t.Error("nil node should evaluate to true (no filter)")
	}
}

// ---- ParseFilterPath ----

func TestParseSCIMFilterPath_Members(t *testing.T) {
	base, inner := ParseFilterPath(`members[value eq "user-abc"]`)
	if base != "members" {
		t.Errorf("expected base=members, got %q", base)
	}
	if inner == nil {
		t.Fatal("expected inner filter node")
	}
	if inner.Op != OpEq {
		t.Errorf("expected op eq, got %v", inner.Op)
	}
	if inner.Value != "user-abc" {
		t.Errorf("expected value=user-abc, got %q", inner.Value)
	}
}

func TestParseSCIMFilterPath_Invalid(t *testing.T) {
	base, inner := ParseFilterPath("members")
	if base != "" || inner != nil {
		t.Error("expected empty result for plain path without brackets")
	}
}

func TestParseSCIMFilterPath_EmailsFilter(t *testing.T) {
	base, inner := ParseFilterPath(`emails[type eq "work"]`)
	if base != "emails" {
		t.Errorf("expected base=emails, got %q", base)
	}
	if inner == nil {
		t.Fatal("expected inner node")
	}
	if inner.Attr != "type" {
		t.Errorf("expected attr=type, got %q", inner.Attr)
	}
}

// ---- backward-compat shim (ParseFlatFilter) ----

func TestParseSCIMFilterShim_Eq(t *testing.T) {
	f := ParseFlatFilter(`userName eq "alice@example.com"`)
	if f == nil {
		t.Fatal("expected non-nil")
	}
	if f.Attr != "username" {
		t.Errorf("expected username, got %q", f.Attr)
	}
	if f.Op != "eq" {
		t.Errorf("expected eq, got %q", f.Op)
	}
	if f.Value != "alice@example.com" {
		t.Errorf("expected alice@example.com, got %q", f.Value)
	}
}

func TestParseSCIMFilterShim_And_ReturnsNil(t *testing.T) {
	// Logical nodes can't map to the flat FlatFilter struct.
	f := ParseFlatFilter(`userName eq "alice" and active eq "true"`)
	if f != nil {
		t.Errorf("expected nil for compound expression, got %+v", f)
	}
}

func TestParseSCIMFilterShim_Co(t *testing.T) {
	f := ParseFlatFilter(`emails.value co "example"`)
	if f == nil {
		t.Fatal("expected non-nil for co")
	}
	if f.Op != "co" {
		t.Errorf("expected op=co, got %q", f.Op)
	}
}

func TestParseSCIMFilterShim_Empty(t *testing.T) {
	if ParseFlatFilter("") != nil {
		t.Error("expected nil for empty input")
	}
}
