package engine

import "stuntgres/internal/engine/functions"

// Query boundaries are explicit: lexical argument walking visits predicate
// left operands but never confuses a child query's local columns with ours.
func walkBoundExpression(node BoundExpr, visit func(BoundExpr)) {
	if node == nil {
		return
	}
	visit(node)
	boundExpressionChildren(node, func(child BoundExpr) { walkBoundExpression(child, visit) })
}

// Enumerate lexical children only; a subquery's query graph is a separate frame.
func boundExpressionChildren(node BoundExpr, walk func(BoundExpr)) {
	args := func(children []BoundExpr) {
		for _, child := range children {
			walk(child)
		}
	}
	switch e := node.(type) {
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
	case currentSchemasExpression:
		walk(e.arg)
	case visibilityInquiryExpression:
		walk(e.arg)
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Value)
		if !e.Local {
			walk(e.Zone)
		}
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
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Pattern)
		walk(e.Escape)
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Left)
		walk(e.Right)
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Left)
		walk(e.Right)
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Pattern)
		walk(e.Escape)
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		for _, arg := range e.Args {
			walk(arg)
		}
	case seriesExpression:
		args(e.args)
	case subscriptsExpression:
		args(e.args)
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
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
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case installedLevenshteinFunctionExpression:
		args(e.Args)
	case installedCryptoFunctionExpression:
		args(e.Args)
	case installedFunctionExpression:
		walk(e.arg)
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		args(e.Args)
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case indexPropertyExpression:
		args(e.children())
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Arg)
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Arguments())
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args[:])
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args[:])
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Arg)
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case deparseExpression:
		args(e.args)
	case commentInquiryExpression:
		args(e.args)
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		walk(e.Arg)
	case advisoryExpression:
		args(e.args)
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		args(e.Args)
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
	}
}

func (b *expressionBinder) ownAggregate(node BoundExpr) (BoundExpr, error) {
	e := node.(aggregateExpression)
	call := b.aggregates[e.slot]
	walkArguments := func(visit func(BoundExpr)) {
		// Ordered-set direct args do not select the aggregate's owner.
		ownerCall := call
		ownerCall.direct = nil
		ownerCall.expressions(func(expr BoundExpr) { walkQueryArgumentExpression(expr, visit) })
	}
	owner, index := b.argumentQueryOwner(walkArguments)
	clause := b.aggregateClause
	if index >= 0 {
		clause = b.queryScopes[index].clause
	}
	var nested BoundExpr
	walkArguments(func(node BoundExpr) {
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
		return nil, expressionError("42803", "aggregate function calls cannot be nested", nested.SourcePosition())
	}
	if call.direct != nil {
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
		walkQueryArgumentExpression(call.direct, func(node BoundExpr) {
			if directError != nil {
				return
			}
			switch n := node.(type) {
			case columnExpression:
				if level(n.frame) > ownerLevel {
					directError = expressionError("42803", "outer-level aggregate cannot contain a lower-level variable in its direct arguments", n.SourcePosition())
				}
			case aggregateExpression:
				if l := level(n.frame); l >= 0 && l <= ownerLevel {
					directError = expressionError("42803", "aggregate function calls cannot be nested", n.SourcePosition())
				}
			case groupingExpression:
				if l := level(n.frame); l >= 0 && l <= ownerLevel {
					directError = expressionError("42803", "aggregate function calls cannot be nested", n.SourcePosition())
				}
			}
		})
		if directError != nil {
			return nil, directError
		}
	}
	// Selected SUM, bitwise and JSON providers resolve children, FILTER and
	// signatures before INSERT VALUES rejection; other aggregates stay unchanged.
	if clause == "INSERT VALUES" {
		switch call.operation {
		case aggregateIntegralSum, aggregateNumericSum, aggregateFloatSum, aggregateMoneySum, aggregateIntervalSum,
			aggregateBitAnd, aggregateBitOr, aggregateBitXor,
			aggregateJSON, aggregateJSONStrict, aggregateJSONArray, aggregateJSONArrayAbsent, aggregateJSONObject, aggregateJSONObjectStrict,
			aggregatePercentileCont, aggregatePercentileDisc, aggregateMode:
			clause = "VALUES"
		}
	}
	switch clause {
	case "SELECT", "HAVING", "ORDER BY", "DISTINCT ON", "window definitions":
	case "window RANGE", "window GROUPS", "window ROWS":
		return nil, expressionError("42803", "aggregate functions are not allowed in "+clause, e.pos)
	case "WHERE", "GROUP BY", "JOIN conditions", "LIMIT", "OFFSET", "RETURNING", "VALUES", "FILTER", "UPDATE", "policy expressions":
		return nil, expressionError("42803", "aggregate functions are not allowed in "+clause, e.pos)
	default:
		return nil, expressionError("0A000", "aggregate calls outside SELECT are unsupported", e.pos)
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
		call.expressions(func(expr BoundExpr) {
			if expr != nil && expr.Properties().Volatility != Immutable {
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
func (b *expressionBinder) argumentQueryOwner(walk func(func(BoundExpr))) (uint32, int) {
	owner, index, local := b.frameID(), -1, false
	walk(func(node BoundExpr) {
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
func walkQueryArgumentExpression(expr BoundExpr, visit func(BoundExpr)) {
	walkBoundExpression(expr, func(node BoundExpr) {
		visit(node)
		if child, ok := node.(subqueryExpression); ok {
			walkQueryArgumentNode(child.queryPlan.Root, visit)
		}
	})
}

func walkQueryArgumentNode(node LogicalNode, visit func(BoundExpr)) {
	queryExpressions(node, func(expr BoundExpr) { walkQueryArgumentExpression(expr, visit) })
	left, right := queryInputs(node)
	if left != nil {
		walkQueryArgumentNode(left, visit)
	}
	if right != nil {
		walkQueryArgumentNode(right, visit)
	}
}
