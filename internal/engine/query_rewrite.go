package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func queryUsesFrame(node plan.Node, frame uint32) bool {
	if node == nil {
		return false
	}
	found := false
	plan.QueryExpressions(node, func(expr expr.Expr) {
		if expressionUsesFrame(expr, frame) {
			found = true
		}
	})
	if found {
		return true
	}
	left, right := plan.QueryInputs(node)
	return queryUsesFrame(left, frame) || queryUsesFrame(right, frame)
}
func expressionUsesFrame(bound expr.Expr, frame uint32) bool {
	found := false
	plan.WalkExpression(bound, func(expr expr.Expr) {
		switch e := expr.(type) {
		case plan.ColumnExpr:
			found = found || e.Frame == frame
		case plan.ReturningWholeRowExpr:
			found = found || e.Frame == frame
		case plan.MergeActionExpr:
			found = found || e.Frame == frame
		case plan.AggregateExpr:
			found = found || e.Frame == frame
		case plan.GroupingExpr:
			found = found || e.Frame == frame
		case *plan.WindowExpr:
			found = found || e.Frame == frame
		case plan.GroupExpr:
			found = found || e.Frame == frame
		case plan.SubqueryExpr:
			found = found || queryUsesFrame(e.QueryPlan.Root, frame)
		}
	})
	return found
}
