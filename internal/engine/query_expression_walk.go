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
		walk(e.provider)
	case updateOutputExpression:
		// The first assignment owns the query; later tuple outputs are leaves.
	case domainValueExpression:
		// Conversion-local VALUE is a lexical leaf, never a query column.
	case assignmentValueExpression:
		walk(e.base)
		walk(e.value)
		for target := e.target; target != nil; target = target.next {
			for _, sub := range target.node.subscripts {
				walk(sub.lower)
				walk(sub.upper)
			}
		}
	case viewQualificationExpression:
		args(e.predicates)
		walk(e.user)
	case groupingExpression:
		args(e.args)
	case rowExpression:
		args(e.fields)
	case fieldExpression:
		walk(e.base)
	case arrayExpression:
		args(e.elements)
	case subscriptExpression:
		walk(e.base)
		for _, s := range e.subscripts {
			walk(s.lower)
			walk(s.upper)
		}
	case returningColumnExpression:
		if e.value != nil {
			walk(e.value)
		} else {
			walk(e.columnExpression)
		}
	case returningWholeRowExpression:
		args(e.members)
	case mergeActionExpression:
		// A lexical owner leaf has no expression children.
	case coercionExpression:
		walk(e.child)
	case collationExpression:
		walk(e.child)
	case arithmeticExpression:
		walk(e.left)
		walk(e.right)
	case bitwiseExpression:
		walk(e.left)
		walk(e.right)
	case rowComparisonExpression:
		for _, pair := range e.comparisons {
			walk(pair.left)
			walk(pair.right)
		}
	case scalarArrayComparisonExpression:
		walk(e.left)
		if e.array != nil {
			walk(e.array)
		}
		args(e.items)
	case comparisonExpression:
		walk(e.left)
		walk(e.right)
	case likeExpression:
		walk(e.left)
		walk(e.right)
	case logicalExpression:
		walk(e.left)
		walk(e.right)
	case distinctExpression:
		walk(e.left)
		walk(e.right)
	case nullTestExpression:
		walk(e.child)
	case truthTestExpression:
		walk(e.child)
	case conditionalExpression:
		args(e.args)
	case installedUUIDOsspFunctionExpression:
		// No argument children.
	case installedFunctionExpression:
		walk(e.Arg)
	case extensionPathsExpression:
		walk(e.arg)
	case sequenceCallExpression:
		args(e.args)
	case caseExpression:
		walk(e.test)
		for _, arm := range e.arms {
			walk(arm.condition)
			walk(arm.result)
		}
		walk(e.otherwise)
	case subqueryExpression:
		for _, comparison := range e.comparisons {
			walk(comparison.left)
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
	call := b.aggregates[e.slot]
	walkArguments := func(visit func(expr.Expr)) {
		// Ordered-set direct args do not select the aggregate's owner.
		ownerCall := call
		ownerCall.direct = nil
		ownerCall.expressions(func(expr expr.Expr) { walkQueryArgumentExpression(expr, visit) })
	}
	owner, index := b.argumentQueryOwner(walkArguments)
	clause := b.aggregateClause
	if index >= 0 {
		clause = b.queryScopes[index].clause
	}
	var nested expr.Expr
	walkArguments(func(node expr.Expr) {
		switch n := node.(type) {
		case aggregateExpression:
			if n.frame == owner && nested == nil {
				nested = n
			}
		case groupingExpression:
			if n.frame == owner && nested == nil {
				nested = n
			}
		}
	})
	if nested != nil {
		return nil, expr.ErrorAt("42803", "aggregate function calls cannot be nested", nested.SourcePosition())
	}
	for _, direct := range call.direct {
		level := func(frame uint32) int {
			if frame == b.frameID() {
				return len(b.queryScopes)
			}
			for i, scope := range b.queryScopes {
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
				if level(n.frame) > ownerLevel {
					directError = expr.ErrorAt("42803", "outer-level aggregate cannot contain a lower-level variable in its direct arguments", n.SourcePosition())
				}
			case aggregateExpression:
				if l := level(n.frame); l >= 0 && l <= ownerLevel {
					directError = expr.ErrorAt("42803", "aggregate function calls cannot be nested", n.SourcePosition())
				}
			case groupingExpression:
				if l := level(n.frame); l >= 0 && l <= ownerLevel {
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
		switch call.operation {
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
		if e.slot == len(b.aggregates)-1 {
			b.aggregates = b.aggregates[:e.slot]
		}
		s := &b.queryScopes[index]
		e.slot = len(s.aggregates)
		immutable := true
		call.expressions(func(bound expr.Expr) {
			if bound != nil && bound.Properties().Volatility != expr.Immutable {
				immutable = false
			}
		})
		if immutable {
			for i, existing := range s.aggregates {
				if b.sameAggregateCall(call, existing) {
					e.slot = i
					break
				}
			}
		}
		if e.slot == len(s.aggregates) {
			s.aggregates = append(s.aggregates, call)
		}
	}
	e.frame = owner
	return e, nil
}

// Pick the nearest visible argument frame, ignoring child-local query frames.
func (b *expressionBinder) argumentQueryOwner(walk func(func(expr.Expr))) (uint32, int) {
	owner, index, local := b.frameID(), -1, false
	walk(func(node expr.Expr) {
		var frame uint32
		switch n := node.(type) {
		case columnExpression:
			frame = n.frame
		case returningWholeRowExpression:
			frame = n.frame
		case mergeActionExpression:
			frame = n.frame
		case aggregateExpression:
			frame = n.frame
		case groupingExpression:
			frame = n.frame
		case groupExpression:
			frame = n.frame
		}
		if frame == 0 {
			return
		}
		if frame == b.frameID() {
			local = true
			return
		}
		for i := len(b.queryScopes) - 1; i >= 0; i-- {
			scope := &b.queryScopes[i]
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
		owner = b.queryScopes[index].frame
	}
	return owner, index
}

func (b *expressionBinder) aggregateCallAt(e aggregateExpression) (aggregateCall, bool) {
	if e.frame == 0 || e.frame == b.frameID() {
		if e.slot >= 0 && e.slot < len(b.aggregates) {
			return b.aggregates[e.slot], true
		}
		return aggregateCall{}, false
	}
	for i := len(b.queryScopes) - 1; i >= 0; i-- {
		scope := &b.queryScopes[i]
		if scope.frame == e.frame && e.slot >= 0 && e.slot < len(scope.aggregates) {
			return scope.aggregates[e.slot], true
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
			walkQueryArgumentNode(child.queryPlan.Root, visit)
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
