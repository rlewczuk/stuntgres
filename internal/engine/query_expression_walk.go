package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
)

// Query boundaries are explicit: lexical argument walking visits predicate
// left operands but never confuses a child query's local columns with ours.
func walkBoundExpression(node expr.Expr, visit func(expr.Expr)) {
	expr.Walk(node, boundExpressionChildren, visit)
}

// Enumerate lexical children only; a subquery's query graph is a separate frame.
func boundExpressionChildren(node expr.Expr, walk func(expr.Expr)) {
	args := func(children []expr.Expr) {
		for _, child := range children {
			walk(child)
		}
	}
	switch e := node.(type) {
	case selectedOperatorExpression:
		walk(e.Provider)
	case updateOutputExpression:
		// The first assignment owns the query; later tuple outputs are leaves.
	case domainValueExpression:
		// Conversion-local VALUE is a lexical leaf, never a query column.
	case assignmentValueExpression:
		walk(e.Base)
		walk(e.Value)
		for target := e.Target; target != nil; target = target.Next {
			for _, sub := range target.Node.Subscripts {
				walk(sub.Lower)
				walk(sub.Upper)
			}
		}
	case viewQualificationExpression:
		args(e.Predicates)
		walk(e.User)
	case groupingExpression:
		args(e.Args)
	case rowExpression:
		args(e.Fields)
	case fieldExpression:
		walk(e.Base)
	case arrayExpression:
		args(e.Elements)
	case subscriptExpression:
		walk(e.Base)
		for _, s := range e.Subscripts {
			walk(s.Lower)
			walk(s.Upper)
		}
	case returningColumnExpression:
		if e.Value != nil {
			walk(e.Value)
		} else {
			walk(e.ColumnExpr)
		}
	case returningWholeRowExpression:
		args(e.Members)
	case mergeActionExpression:
		// A lexical owner leaf has no expression children.
	case coercionExpression:
		walk(e.Child)
	case collationExpression:
		walk(e.Child)
	case arithmeticExpression:
		walk(e.Left)
		walk(e.Right)
	case bitwiseExpression:
		walk(e.Left)
		walk(e.Right)
	case rowComparisonExpression:
		for _, pair := range e.Comparisons {
			walk(pair.Left)
			walk(pair.Right)
		}
	case scalarArrayComparisonExpression:
		walk(e.Left)
		if e.Array != nil {
			walk(e.Array)
		}
		args(e.Items)
	case comparisonExpression:
		walk(e.Left)
		walk(e.Right)
	case likeExpression:
		walk(e.Left)
		walk(e.Right)
	case logicalExpression:
		walk(e.Left)
		walk(e.Right)
	case distinctExpression:
		walk(e.Left)
		walk(e.Right)
	case nullTestExpression:
		walk(e.Child)
	case truthTestExpression:
		walk(e.Child)
	case conditionalExpression:
		args(e.Args)
	case installedUUIDOsspFunctionExpression:
		// No argument children.
	case installedFunctionExpression:
		walk(e.Arg)
	case extensionPathsExpression:
		walk(e.arg)
	case caseExpression:
		walk(e.Test)
		for _, arm := range e.Arms {
			walk(arm.Condition)
			walk(arm.Result)
		}
		walk(e.Otherwise)
	case subqueryExpression:
		for _, comparison := range e.Comparisons {
			walk(comparison.Left)
		}
	case functions.Timezone:
		// Known divergence kept from before the node protocol: PostgreSQL walks
		// timezone(zone, value) zone first, which decides the reported column in
		// GROUP BY and nested-aggregate errors. Children follow PostgreSQL.
		walk(e.Value)
		if !e.Local {
			walk(e.Zone)
		}
	case expr.Composite:
		// Last, so the explicit cases above keep precedence over embedded nodes.
		args(e.Children())
	}
}

func (b *expressionBinder) ownAggregate(node expr.Expr) (expr.Expr, error) {
	e := node.(aggregateExpression)
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
		case aggregateExpression:
			if n.Frame == owner && nested == nil {
				nested = n
			}
		case groupingExpression:
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
			case columnExpression:
				if level(n.Frame) > ownerLevel {
					directError = expr.ErrorAt("42803", "outer-level aggregate cannot contain a lower-level variable in its direct arguments", n.SourcePosition())
				}
			case aggregateExpression:
				if l := level(n.Frame); l >= 0 && l <= ownerLevel {
					directError = expr.ErrorAt("42803", "aggregate function calls cannot be nested", n.SourcePosition())
				}
			case groupingExpression:
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
		case columnExpression:
			frame = n.Frame
		case returningWholeRowExpression:
			frame = n.Frame
		case mergeActionExpression:
			frame = n.Frame
		case aggregateExpression:
			frame = n.Frame
		case groupingExpression:
			frame = n.Frame
		case groupExpression:
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

func (b *expressionBinder) aggregateCallAt(e aggregateExpression) (aggregateCall, bool) {
	if e.Frame == 0 || e.Frame == b.frameID() {
		if e.Slot >= 0 && e.Slot < len(b.grouping.aggregates) {
			return b.grouping.aggregates[e.Slot], true
		}
		return aggregateCall{}, false
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		scope := &b.query.scopes[i]
		if scope.frame == e.Frame && e.Slot >= 0 && e.Slot < len(scope.aggregates) {
			return scope.aggregates[e.Slot], true
		}
	}
	return aggregateCall{}, false
}

// Aggregate ownership crosses sublinks, but only columns/aggregates in a
// visible ancestor frame participate. Child-local frames are ignored by the
// owner selection above; nesting is checked against that selected owner.
func walkQueryArgumentExpression(bound expr.Expr, visit func(expr.Expr)) {
	walkBoundExpression(bound, func(node expr.Expr) {
		visit(node)
		if child, ok := node.(subqueryExpression); ok {
			walkQueryArgumentNode(child.QueryPlan.Root, visit)
		}
	})
}

func walkQueryArgumentNode(node LogicalNode, visit func(expr.Expr)) {
	queryExpressions(node, func(expr expr.Expr) { walkQueryArgumentExpression(expr, visit) })
	left, right := queryInputs(node)
	if left != nil {
		walkQueryArgumentNode(left, visit)
	}
	if right != nil {
		walkQueryArgumentNode(right, visit)
	}
}
