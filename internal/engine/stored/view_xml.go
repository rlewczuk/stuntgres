package stored

import (
	"fmt"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Same-Usage casts are erased by ordinary binding. A replayed standard CAST
// starts before its surviving operand, unlike postfix syntax. Follow only the
// exact frozen annotations of such erased boundaries, retaining the original
// source position and the accepted operand's complete Usage as authority.
func (v *View) xmlSourceOccurrence(path analyze.ViewLiteralPath, raw parser.Expr, bound expr.Expr) bool {
	if raw == nil || bound == nil {
		return false
	}
	path.Argument = 0
	path.Node++ // The XML operation's first written operand.
	for raw.SourcePosition() != bound.SourcePosition() {
		cast, ok := raw.(parser.CastExpr)
		if !ok || cast.FrozenMethod != "" {
			return false
		}
		if usage, captured := v.castUsages[path]; !captured || usage != bound.ResultUsage() {
			return false
		}
		raw = cast.Child
		if raw == nil {
			return false
		}
		path.Node++
	}
	return true
}

// Authenticate syntax against the actual accepted node in the owning frame,
// independently of the presentation target map. Raw type names are not authority.
func (v *View) capturedXML(path analyze.ViewLiteralPath, raw parser.Expr) (sqltype.Usage, error) {
	found := false
	var usage sqltype.Usage
	var failure error
	check := func(node expr.Expr) {
		match := false
		switch n := node.(type) {
		case functions.XMLDocumentPredicate:
			if r, ok := raw.(parser.XMLDocumentPredicateExpr); ok && reflect.DeepEqual(n.Syntax, r) && n.SourcePosition() == r.Pos && n.Negated == r.Negated {
				written, err := plan.XMLSourceOperand(n.Child, n.Pos)
				match = err == nil && v.xmlSourceOccurrence(path, r.Child, written)
				if match {
					failure = n.ValidateStoredProvider()
				}
			}
		case functions.XMLParse:
			if r, ok := raw.(parser.XMLParseExpr); ok && reflect.DeepEqual(n.Syntax, r) && n.SourcePosition() == r.Pos {
				written, err := plan.XMLSourceOperand(n.Child, n.Pos)
				match = err == nil && v.xmlSourceOccurrence(path, r.Child, written) && n.Document == r.Document && n.Whitespace == r.Whitespace
				if match {
					failure = n.ValidateStoredProvider()
				}
			}
		case plan.CoercionExpr:
			if !n.XMLSerializeResult {
				break
			}
			source, target, err := plan.XMLSerialization(n)
			if err != nil {
				failure = err
				return
			}
			if r, ok := raw.(parser.XMLSerializeExpr); ok && reflect.DeepEqual(source.Syntax, r) && n.SourcePosition() == r.Pos {
				match = source.Document == r.Document && source.Indent == r.Indent
				if match {
					if found && usage != target {
						failure = fmt.Errorf("stuntgres: ambiguous frozen XML occurrence")
					}
					usage, found = target, true
				}
			}
			return
		}
		if !match {
			return
		}
		if found && usage != node.ResultUsage() {
			failure = fmt.Errorf("stuntgres: ambiguous frozen XML occurrence")
		}
		usage = node.ResultUsage()
		found = true
	}
	var walk func(plan.Node)
	walk = func(node plan.Node) {
		if node == nil || failure != nil {
			return
		}
		if p, ok := node.(*plan.ProjectNode); ok && p.Frame != path.Query {
			return
		}
		plan.QueryExpressions(node, func(bound expr.Expr) { plan.WalkExpression(bound, check) })
		left, right := plan.QueryInputs(node)
		walk(left)
		walk(right)
	}
	walk(v.frames[path.Query])
	if failure != nil {
		return sqltype.Usage{}, failure
	}
	if !found {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: missing frozen XML occurrence")
	}
	return usage, nil
}
