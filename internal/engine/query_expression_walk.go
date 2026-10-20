package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
)

func (b *expressionBinder) ownAggregate(node expr.Expr) (expr.Expr, error) {
	e := node.(plan.AggregateExpr)
	call := b.grouping.aggregates[e.Slot]
	walkArguments := func(visit func(expr.Expr)) {
		// Ordered-set direct args do not select the aggregate's owner.
		ownerCall := call
		ownerCall.Direct = nil
		ownerCall.Expressions(func(expr expr.Expr) { walkQueryArgumentExpression(expr, visit) })
	}
	owner, index := b.argumentQueryOwner(walkArguments)
	clause := b.clause.aggregateClause
	if index >= 0 {
		clause = b.query.scopes[index].clause
	}
	var nested expr.Expr
	walkArguments(func(node expr.Expr) {
		switch n := node.(type) {
		case plan.AggregateExpr:
			if n.Frame == owner && nested == nil {
				nested = n
			}
		case plan.GroupingExpr:
			if n.Frame == owner && nested == nil {
				nested = n
			}
		}
	})
	if nested != nil {
		return nil, expr.ErrorAt("42803", "aggregate function calls cannot be nested", nested.SourcePosition())
	}
	for _, direct := range call.Direct {
		level := func(frame uint32) int {
			if frame == b.frameID() {
				return len(b.query.scopes)
			}
			for i, scope := range b.query.scopes {
				if scope.frame == frame && scope.allowed {
					return i
				}
			}
			return -1
		}
		ownerLevel := level(owner)
		var directError error
		walkQueryArgumentExpression(direct, func(node expr.Expr) {
			if directError != nil {
				return
			}
			switch n := node.(type) {
			case plan.ColumnExpr:
				if level(n.Frame) > ownerLevel {
					directError = expr.ErrorAt("42803", "outer-level aggregate cannot contain a lower-level variable in its direct arguments", n.SourcePosition())
				}
			case plan.AggregateExpr:
				if l := level(n.Frame); l >= 0 && l <= ownerLevel {
					directError = expr.ErrorAt("42803", "aggregate function calls cannot be nested", n.SourcePosition())
				}
			case plan.GroupingExpr:
				if l := level(n.Frame); l >= 0 && l <= ownerLevel {
					directError = expr.ErrorAt("42803", "aggregate function calls cannot be nested", n.SourcePosition())
				}
			}
		})
		if directError != nil {
			return nil, directError
		}
	}
	// Selected providers resolve children, FILTER and signatures before INSERT
	// VALUES rejection; this includes the installed scalar-array wrapper.
	if clause == "INSERT VALUES" {
		switch call.Operation {
		case functions.AggregateIntegralSum, functions.AggregateNumericSum, functions.AggregateFloatSum, functions.AggregateMoneySum, functions.AggregateIntervalSum,
			functions.AggregateBitAnd, functions.AggregateBitOr, functions.AggregateBitXor, functions.AggregateArrayScalar,
			functions.AggregateJSON, functions.AggregateJSONStrict, functions.AggregateJSONArray, functions.AggregateJSONArrayAbsent, functions.AggregateJSONObject, functions.AggregateJSONObjectStrict,
			functions.AggregatePercentileCont, functions.AggregatePercentileDisc, functions.AggregateMode,
			functions.AggregateHypotheticalRank, functions.AggregateHypotheticalDenseRank, functions.AggregateHypotheticalPercentRank, functions.AggregateHypotheticalCumeDist:
			clause = "VALUES"
		}
	}
	switch clause {
	case "SELECT", "HAVING", "ORDER BY", "DISTINCT ON", "window definitions":
	case "window RANGE", "window GROUPS", "window ROWS":
		return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in "+clause, e.Pos)
	case "WHERE", "GROUP BY", "JOIN conditions", "LIMIT", "OFFSET", "RETURNING", "VALUES", "FILTER", "UPDATE", "policy expressions":
		return nil, expr.ErrorAt("42803", "aggregate functions are not allowed in "+clause, e.Pos)
	default:
		return nil, expr.ErrorAt("0A000", "aggregate calls outside SELECT are unsupported", e.Pos)
	}
	if index >= 0 {
		// This call was registered locally by the existing provider binder.
		// Removing its last registration transfers execution to its lexical owner.
		if e.Slot == len(b.grouping.aggregates)-1 {
			b.grouping.aggregates = b.grouping.aggregates[:e.Slot]
		}
		s := &b.query.scopes[index]
		e.Slot = len(s.aggregates)
		immutable := true
		call.Expressions(func(bound expr.Expr) {
			if bound != nil && bound.Properties().Volatility != expr.Immutable {
				immutable = false
			}
		})
		if immutable {
			for i, existing := range s.aggregates {
				if b.sameAggregateCall(call, existing) {
					e.Slot = i
					break
				}
			}
		}
		if e.Slot == len(s.aggregates) {
			s.aggregates = append(s.aggregates, call)
		}
	}
	e.Frame = owner
	return e, nil
}

// Pick the nearest visible argument frame, ignoring child-local query frames.
func (b *expressionBinder) argumentQueryOwner(walk func(func(expr.Expr))) (uint32, int) {
	owner, index, local := b.frameID(), -1, false
	walk(func(node expr.Expr) {
		var frame uint32
		switch n := node.(type) {
		case plan.ColumnExpr:
			frame = n.Frame
		case plan.ReturningWholeRowExpr:
			frame = n.Frame
		case plan.MergeActionExpr:
			frame = n.Frame
		case plan.AggregateExpr:
			frame = n.Frame
		case plan.GroupingExpr:
			frame = n.Frame
		case plan.GroupExpr:
			frame = n.Frame
		}
		if frame == 0 {
			return
		}
		if frame == b.frameID() {
			local = true
			return
		}
		for i := len(b.query.scopes) - 1; i >= 0; i-- {
			scope := &b.query.scopes[i]
			if scope.allowed && scope.frame == frame {
				if i > index {
					index = i
				}
				break
			}
		}
	})
	if local {
		index = -1
	}
	if index >= 0 {
		owner = b.query.scopes[index].frame
	}
	return owner, index
}

func (b *expressionBinder) aggregateCallAt(e plan.AggregateExpr) (plan.AggregateCall, bool) {
	if e.Frame == 0 || e.Frame == b.frameID() {
		if e.Slot >= 0 && e.Slot < len(b.grouping.aggregates) {
			return b.grouping.aggregates[e.Slot], true
		}
		return plan.AggregateCall{}, false
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		scope := &b.query.scopes[i]
		if scope.frame == e.Frame && e.Slot >= 0 && e.Slot < len(scope.aggregates) {
			return scope.aggregates[e.Slot], true
		}
	}
	return plan.AggregateCall{}, false
}

// Aggregate ownership crosses sublinks, but only columns/aggregates in a
// visible ancestor frame participate. Child-local frames are ignored by the
// owner selection above; nesting is checked against that selected owner.
func walkQueryArgumentExpression(bound expr.Expr, visit func(expr.Expr)) {
	plan.WalkExpression(bound, func(node expr.Expr) {
		visit(node)
		if child, ok := node.(plan.SubqueryExpr); ok {
			walkQueryArgumentNode(child.QueryPlan.Root, visit)
		}
	})
}

func walkQueryArgumentNode(node plan.Node, visit func(expr.Expr)) {
	plan.QueryExpressions(node, func(expr expr.Expr) { walkQueryArgumentExpression(expr, visit) })
	left, right := plan.QueryInputs(node)
	if left != nil {
		walkQueryArgumentNode(left, visit)
	}
	if right != nil {
		walkQueryArgumentNode(right, visit)
	}
}
