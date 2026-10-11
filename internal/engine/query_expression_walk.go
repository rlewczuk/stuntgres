package engine

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
	case viewQualificationExpression:
		args(e.predicates)
		walk(e.user)
	case groupingExpression:
		args(e.args)
	case arrayExpression:
		args(e.elements)
	case subscriptExpression:
		walk(e.base)
		for _, s := range e.subscripts {
			walk(s.lower)
			walk(s.upper)
		}
	case updateOldExpression:
		walk(e.columnExpression)
	case deleteNewExpression:
		walk(e.columnExpression)
	case coercionExpression:
		walk(e.child)
	case collationExpression:
		walk(e.child)
	case currentSchemasExpression:
		walk(e.arg)
	case visibilityInquiryExpression:
		walk(e.arg)
	case timeZoneExpression:
		walk(e.value)
		if !e.local {
			walk(e.zone)
		}
	case arithmeticExpression:
		walk(e.left)
		walk(e.right)
	case bitwiseExpression:
		walk(e.left)
		walk(e.right)
	case comparisonExpression:
		walk(e.left)
		walk(e.right)
	case likeExpression:
		walk(e.left)
		walk(e.right)
	case likeEscapeExpression:
		walk(e.pattern)
		walk(e.escape)
	case posixExpression:
		walk(e.left)
		walk(e.right)
	case similarExpression:
		walk(e.left)
		walk(e.right)
	case similarEscapeExpression:
		walk(e.pattern)
		walk(e.escape)
	case posixSubstringExpression:
		for _, arg := range e.args {
			walk(arg)
		}
	case similarSubstringExpression:
		for _, arg := range e.args {
			walk(arg)
		}
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
	case binaryExpression:
		args(e.args)
	case stringExpression:
		args(e.args)
	case formatExpression:
		args(e.args)
	case indexPropertyExpression:
		args(e.children())
	case datetimeJustifyExpression:
		walk(e.arg)
	case datetimeExtractExpression:
		args(e.children())
	case datetimeOverlapsExpression:
		args(e.children())
	case datetimeBinExpression:
		args(e.children())
	case datetimeTruncExpression:
		args(e.children())
	case datetimeZoneCalendarExpression:
		args(e.children())
	case datetimeAgeFiniteExpression:
		args(e.children())
	case datetimeMakeExpression:
		args(e.children())
	case datetimeFormattingExpression:
		args(e.args[:])
	case numericFormattingExpression:
		args(e.args[:])
	case enumFunctionExpression:
		args(e.args)
	case mathExpression:
		args(e.args)
	case deparseExpression:
		args(e.args)
	case commentInquiryExpression:
		args(e.args)
	case nullCountExpression:
		args(e.args)
	case randomExpression:
		args(e.args)
	case advisoryExpression:
		args(e.args)
	case uuidGenerationExpression:
		args(e.args)
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
		call.expressions(func(expr BoundExpr) { walkQueryArgumentExpression(expr, visit) })
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
	// SUM resolves its children and signature before the INSERT VALUES restriction.
	// Keep other aggregates' incumbent clause handling unchanged.
	if clause == "INSERT VALUES" {
		switch call.operation {
		case aggregateIntegralSum, aggregateNumericSum, aggregateFloatSum:
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
