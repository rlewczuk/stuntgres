package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// OverlapsInlineSafe reports whether an OVERLAPS call may be inlined.
// Only duration starts occur twice in the SQL wrapper body. Released inline
// safety tests apply to those slots, not volatile single-use durations/ends.
func OverlapsInlineSafe(e functions.DatetimeOverlaps) bool {
	for pair, duration := range e.Plan.Duration() {
		if !duration {
			continue
		}
		start := e.Args[pair*2]
		if PlannedVolatility(start) == expr.Volatile {
			return false
		}
		safe, cost := true, 0
		plan.WalkExpression(start, func(node expr.Expr) {
			if _, ok := node.(plan.SubqueryExpr); ok {
				safe = false
			}
			cost = min(11, cost+OverlapsNodeCost(node))
		})
		if !safe || cost > 10 {
			return false
		}
	}
	return true
}

// OverlapsNodeCost is one node's cost toward the inline threshold.
// Costs are released builtin procost units, not AST node counts. Structural
// boolean/CASE nodes cost zero; only function-backed work adds to the threshold.
func OverlapsNodeCost(node expr.Expr) int {
	switch n := node.(type) {
	case plan.LiteralExpr, plan.ParameterExpr, plan.ColumnExpr, plan.GroupExpr, plan.AggregateExpr, plan.GroupingExpr, plan.CollationExpr, plan.LogicalExpr, plan.NullTestExpr, plan.TruthTestExpr, plan.ReturningColumnExpr, plan.ReturningWholeRowExpr:
		return 0
	case plan.CaseExpr:
		if n.Test != nil {
			return len(n.Arms)
		}
		return 0
	case plan.ConditionalExpr:
		if n.Kind == parser.NullIf || n.Kind == parser.Greatest || n.Kind == parser.Least {
			return 1
		}
		return 0
	case plan.CoercionExpr:
		cost := n.Plan.EvaluationFunctionCount()
		if n.ResultUsage().Type.IsDomain() {
			cost++
		}
		return cost
	case expr.CallCoster:
		return n.CallCost()
	case functions.DatetimeAgeFinite:
		if n.Plan.Name() == "age" && n.Count == 1 {
			return 3
		}
		return 1
	case functions.DatetimeExtract:
		if n.Plan.Name() == "date_part" && n.Plan.InputID() == sqltype.Date {
			return 2
		}
		return 1
	case functions.DatetimeOverlaps:
		cost := 1
		if !n.WrapperContext {
			for pair, duration := range n.Plan.Duration() {
				if duration && n.Folded[pair].Type() == nil {
					cost += 1 + OverlapsTreeCost(n.Args[pair*2])
				}
			}
		}
		return cost
	default:
		return 1
	}
}

// OverlapsTreeCost is a tree's cost toward the inline threshold, capped at 11.
func OverlapsTreeCost(node expr.Expr) int {
	cost := 0
	plan.WalkExpression(node, func(n expr.Expr) { cost = min(11, cost+OverlapsNodeCost(n)) })
	return cost
}
