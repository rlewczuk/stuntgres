package engine

// plannedVolatility inspects the physical surviving tree. Analysis properties
// deliberately retain dependence from eliminated CASE/COALESCE arms.
func plannedVolatility(node BoundExpr) Volatility {
	if node == nil {
		return Immutable
	}
	merge := func(a, b Volatility) Volatility {
		if b > a {
			return b
		}
		return a
	}
	children := func(args ...BoundExpr) Volatility {
		v := Immutable
		for _, a := range args {
			v = merge(v, plannedVolatility(a))
		}
		return v
	}
	session := func(v Volatility, dependent bool) Volatility {
		if dependent {
			return merge(v, Stable)
		}
		return v
	}
	switch n := node.(type) {
	case literalExpression, columnExpression:
		return Immutable
	case collationExpression:
		return plannedVolatility(n.child)
	case coercionExpression:
		return session(plannedVolatility(n.child), n.plan.SessionDependent())
	case comparisonExpression:
		return session(children(n.left, n.right), n.predicate.SessionDependent())
	case arithmeticExpression:
		return session(children(n.left, n.right), n.provider.SessionDependent())
	case bitwiseExpression:
		return children(n.left, n.right)
	case likeExpression:
		return children(n.left, n.right)
	case likeEscapeExpression:
		return children(n.pattern, n.escape)
	case nullTestExpression:
		return plannedVolatility(n.child)
	case truthTestExpression:
		return plannedVolatility(n.child)
	case logicalExpression:
		return children(n.left, n.right)
	case distinctExpression:
		return session(children(n.left, n.right), n.equality.SessionDependent())
	case conditionalExpression:
		return session(children(n.args...), n.comparison.SessionDependent())
	case binaryExpression:
		return children(n.args...)
	case datetimeMakeExpression:
		return session(children(n.children()...), n.plan.SessionDependent())
	case datetimeFormattingExpression:
		return session(children(n.args[:]...), true)
	case stringExpression:
		return session(children(n.args...), n.plan.SessionDependent())
	case timeZoneExpression:
		return merge(children(n.value, n.zone), n.intrinsicVolatility())
	case mathExpression:
		return children(n.args...)
	case nullCountExpression:
		return children(n.args...)
	case formatExpression:
		return merge(Stable, children(n.args...))
	case caseExpression:
		v := children(n.test, n.otherwise)
		for _, arm := range n.arms {
			v = merge(v, children(arm.condition, arm.result))
			if n.test != nil {
				v = session(v, arm.testPlan.SessionDependent() || arm.equality.SessionDependent())
			}
		}
		return v
	default:
		return node.Properties().Volatility
	}
}
