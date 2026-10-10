package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func overlapsErrorContext(err error, context string) error {
	if d, ok := err.(*sqlstate.Error); ok && d.Context == "" {
		copy := *d
		copy.Context = context
		return &copy
	}
	return err
}

// Imported parameters remain Execute-owned. This predicate inspects structure
// and availability only, never evaluates a child again or freezes its value.
func overlapsExecutionConstant(node BoundExpr, c *EvalContext) bool {
	switch n := node.(type) {
	case literalExpression:
		return true
	case parameterExpression:
		return n.number > 0 && n.number <= len(c.Parameters)
	case columnExpression, groupExpression, aggregateExpression, subqueryExpression:
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
func overlapsInlineSafe(e datetimeOverlapsExpression) bool {
	for pair, duration := range e.plan.Duration() {
		if !duration {
			continue
		}
		start := e.args[pair*2]
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
	case literalExpression, parameterExpression, columnExpression, groupExpression, aggregateExpression, collationExpression, logicalExpression, nullTestExpression, truthTestExpression, updateOldExpression, deleteNewExpression:
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
	case visibilityInquiryExpression:
		return 10
	case commentInquiryExpression:
		return 100
	case datetimeAgeFiniteExpression:
		if n.plan.Name() == "age" && n.count == 1 {
			return 3
		}
		return 1
	case datetimeExtractExpression:
		if n.plan.Name() == "date_part" && n.plan.InputID() == sqltype.Date {
			return 2
		}
		return 1
	case datetimeOverlapsExpression:
		cost := 1
		if !n.wrapperContext {
			for pair, duration := range n.plan.Duration() {
				if duration && n.folded[pair].Type() == nil {
					cost += 1 + overlapsTreeCost(n.args[pair*2])
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

func (e datetimeOverlapsExpression) effectiveContext(c *EvalContext, pair int) string {
	if pair < 0 {
		return ""
	}
	constant := [4]bool{}
	all := true
	for i, arg := range e.args {
		constant[i] = overlapsExecutionConstant(arg, c)
		all = all && constant[i]
	}
	if !e.plan.SessionDependent() && all || e.wrapperContext {
		return `SQL function "overlaps" statement 1`
	}
	if !e.plan.SessionDependent() && constant[pair*2] && constant[pair*2+1] {
		return `SQL function "overlaps" during inlining`
	}
	return ""
}

// Simplification of caller children precedes SQL wrapper evaluation/inlining.
// A partial constant body addition can fail during inlining even when another
// pair uses table columns. Stable zoned addition is never ordinarily folded.
func simplifyOverlapsRoute(c *EvalContext, e datetimeOverlapsExpression) (datetimeOverlapsExpression, bool, error) {
	old := e.wrapperContext
	oldFolded := e.folded
	e.wrapperContext = !overlapsInlineSafe(e)
	all := true
	for _, arg := range e.args {
		if _, ok := arg.(literalExpression); !ok {
			all = false
			break
		}
	}
	if all && !e.plan.SessionDependent() {
		e.wrapperContext = true
		return e, e.wrapperContext != old, nil
	}
	if !e.wrapperContext && !e.plan.SessionDependent() {
		for pair, duration := range e.plan.Duration() {
			if !duration {
				continue
			}
			start, a := e.args[pair*2].(literalExpression)
			interval, b := e.args[pair*2+1].(literalExpression)
			if !a || !b {
				continue
			}
			if e.folded[pair].Type() != nil {
				continue
			}
			end, err := e.plan.DurationEnd(c.Context, pair, start.value, interval.value, c.Input)
			if err != nil {
				return e, false, overlapsErrorContext(err, `SQL function "overlaps" during inlining`)
			}
			e.folded[pair] = end
		}
	}
	return e, e.wrapperContext != old || e.folded != oldFolded, nil
}
