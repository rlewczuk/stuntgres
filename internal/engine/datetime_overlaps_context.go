package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Imported parameters remain Execute-owned. This predicate inspects structure
// and availability only, never evaluates a child again or freezes its value.
func overlapsExecutionConstant(node BoundExpr, c *EvalContext) bool {
	switch n := node.(type) {
	case literalExpression:
		return true
	case parameterExpression:
		return n.number > 0 && n.number <= len(c.Parameters)
	case columnExpression, groupExpression, aggregateExpression, groupingExpression, subqueryExpression:
		return false
	}
	if plannedVolatility(node) != Immutable {
		return false
	}
	found, constant := false, true
	boundExpressionChildren(node, func(child BoundExpr) { found = true; constant = constant && overlapsExecutionConstant(child, c) })
	return found && constant
}

// Only duration starts occur twice in the SQL wrapper body. Released inline
// safety tests apply to those slots, not volatile single-use durations/ends.
func overlapsInlineSafe(e functions.DatetimeOverlaps[BoundExpr, ExprContext, ExprProperties]) bool {
	for pair, duration := range e.Plan.Duration() {
		if !duration {
			continue
		}
		start := e.Args[pair*2]
		if plannedVolatility(start) == Volatile {
			return false
		}
		safe, cost := true, 0
		walkBoundExpression(start, func(node BoundExpr) {
			if _, ok := node.(subqueryExpression); ok {
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
func overlapsNodeCost(node BoundExpr) int {
	switch n := node.(type) {
	case literalExpression, parameterExpression, columnExpression, groupExpression, aggregateExpression, groupingExpression, collationExpression, logicalExpression, nullTestExpression, truthTestExpression, returningColumnExpression, returningWholeRowExpression:
		return 0
	case caseExpression:
		if n.test != nil {
			return len(n.arms)
		}
		return 0
	case conditionalExpression:
		if n.kind == parser.NullIf || n.kind == parser.Greatest || n.kind == parser.Least {
			return 1
		}
		return 0
	case coercionExpression:
		cost := n.plan.EvaluationFunctionCount()
		if n.ResultUsage().Type.IsDomain() {
			cost++
		}
		return cost
	case catalogUserNameExpression, visibilityInquiryExpression:
		return 10
	case commentInquiryExpression:
		return 100
	case functions.DatetimeAgeFinite[BoundExpr, ExprContext, ExprProperties]:
		if n.Plan.Name() == "age" && n.Count == 1 {
			return 3
		}
		return 1
	case functions.DatetimeExtract[BoundExpr, ExprContext, ExprProperties]:
		if n.Plan.Name() == "date_part" && n.Plan.InputID() == sqltype.Date {
			return 2
		}
		return 1
	case functions.DatetimeOverlaps[BoundExpr, ExprContext, ExprProperties]:
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

func overlapsTreeCost(node BoundExpr) int {
	cost := 0
	walkBoundExpression(node, func(n BoundExpr) { cost = min(11, cost+overlapsNodeCost(n)) })
	return cost
}

// Simplification of caller children precedes SQL wrapper evaluation/inlining.
// A partial constant body addition can fail during inlining even when another
// pair uses table columns. Stable zoned addition is never ordinarily folded.
func simplifyOverlapsRoute(c *EvalContext, e functions.DatetimeOverlaps[BoundExpr, ExprContext, ExprProperties]) (functions.DatetimeOverlaps[BoundExpr, ExprContext, ExprProperties], bool, error) {
	old := e.WrapperContext
	oldFolded := e.Folded
	e.WrapperContext = !overlapsInlineSafe(e)
	all := true
	for _, arg := range e.Args {
		if _, ok := arg.(literalExpression); !ok {
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
			start, a := e.Args[pair*2].(literalExpression)
			interval, b := e.Args[pair*2+1].(literalExpression)
			if !a || !b {
				continue
			}
			if e.Folded[pair].Type() != nil {
				continue
			}
			if err := admitOverlapsPair(c, e, pair); err != nil {
				return e, false, err
			}
			end, err := e.Plan.DurationEnd(c.Context, pair, start.value, interval.value, c.Input)
			if err != nil {
				return e, false, functions.OverlapsErrorContext(err, `SQL function "overlaps" during inlining`)
			}
			e.Folded[pair] = end
		}
	}
	return e, e.WrapperContext != old || e.Folded != oldFolded, nil
}
