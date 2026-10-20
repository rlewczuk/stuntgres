package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Only duration starts occur twice in the SQL wrapper body. Released inline
// safety tests apply to those slots, not volatile single-use durations/ends.
func overlapsInlineSafe(e functions.DatetimeOverlaps) bool {
	for pair, duration := range e.Plan.Duration() {
		if !duration {
			continue
		}
		start := e.Args[pair*2]
		if exec.PlannedVolatility(start) == expr.Volatile {
			return false
		}
		safe, cost := true, 0
		plan.WalkExpression(start, func(node expr.Expr) {
			if _, ok := node.(plan.SubqueryExpr); ok {
				safe = false
			}
			cost = min(11, cost+overlapsNodeCost(node))
		})
		if !safe || cost > 10 {
			return false
		}
	}
	return true
}

// Costs are released builtin procost units, not AST node counts. Structural
// boolean/CASE nodes cost zero; only function-backed work adds to the threshold.
func overlapsNodeCost(node expr.Expr) int {
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
					cost += 1 + overlapsTreeCost(n.Args[pair*2])
				}
			}
		}
		return cost
	default:
		return 1
	}
}

func overlapsTreeCost(node expr.Expr) int {
	cost := 0
	plan.WalkExpression(node, func(n expr.Expr) { cost = min(11, cost+overlapsNodeCost(n)) })
	return cost
}

// Simplification of caller children precedes SQL wrapper evaluation/inlining.
// A partial constant body addition can fail during inlining even when another
// pair uses table columns. Stable zoned addition is never ordinarily folded.
func simplifyOverlapsRoute(c *EvalContext, e functions.DatetimeOverlaps) (functions.DatetimeOverlaps, bool, error) {
	old := e.WrapperContext
	oldFolded := e.Folded
	e.WrapperContext = !overlapsInlineSafe(e)
	all := true
	for _, arg := range e.Args {
		if _, ok := arg.(plan.LiteralExpr); !ok {
			all = false
			break
		}
	}
	if all && !e.Plan.SessionDependent() {
		e.WrapperContext = true
		return e, e.WrapperContext != old, nil
	}
	if !e.WrapperContext && !e.Plan.SessionDependent() {
		for pair, duration := range e.Plan.Duration() {
			if !duration {
				continue
			}
			start, a := e.Args[pair*2].(plan.LiteralExpr)
			interval, b := e.Args[pair*2+1].(plan.LiteralExpr)
			if !a || !b {
				continue
			}
			if e.Folded[pair].Type() != nil {
				continue
			}
			if err := admitOverlapsPair(c, e, pair); err != nil {
				return e, false, err
			}
			end, err := e.Plan.DurationEnd(c.Context, pair, start.Value, interval.Value, c.Input)
			if err != nil {
				return e, false, functions.OverlapsErrorContext(err, `SQL function "overlaps" during inlining`)
			}
			e.Folded[pair] = end
		}
	}
	return e, e.WrapperContext != old || e.Folded != oldFolded, nil
}
