package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// A left-only WHERE restriction belongs before a correlated function invocation:
// excluded predecessor rows must not open its cursor. Move the complete predicate
// once in an execution-private copy, without changing canonical/view literal
// paths, outer joins, merged columns or expression boundaries. Nil declines.
func FunctionFromWhere(root plan.Node, predicate expr.Expr) *plan.JoinNode {
	join, ok := root.(*plan.JoinNode)
	if !ok || join.Kind != parser.JoinInner || !join.CorrelatedRight || len(join.Merges) != 0 {
		return nil
	}
	if _, ok := join.Right.(*plan.FunctionFromNode); !ok {
		return nil
	}
	if !plan.FunctionFromRestriction(join, predicate) {
		return nil
	}
	copy := *join
	copy.Left = &plan.FilterNode{QuerySchema: join.Left.Schema(), Child: join.Left, Predicate: predicate}
	return &copy
}
