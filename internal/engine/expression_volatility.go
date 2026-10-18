package engine

import "stuntgres/internal/engine/expr"

// plannedVolatility inspects the physical surviving tree. Analysis properties
// deliberately retain dependence from eliminated CASE/COALESCE arms.
func plannedVolatility(node expr.Expr) expr.Volatility {
	return plannedPhysicalVolatility(node, false)
}

// COPY's supported nextval defaults are batch-safe, but their argument trees
// are still inspected. Other consumers retain ordinary physical volatility.
func plannedPhysicalVolatility(node expr.Expr, copyNextval bool) expr.Volatility {
	return plannedNodeVolatility(node, copyNextval, false)
}

// Use the same provider/signature authority without walking operand recipes.
func plannedIntrinsicVolatility(node expr.Expr) expr.Volatility {
	return plannedNodeVolatility(node, false, true)
}

func plannedNodeVolatility(node expr.Expr, copyNextval, intrinsicOnly bool) expr.Volatility {
	if node == nil {
		return expr.Immutable
	}
	merge := func(a, b expr.Volatility) expr.Volatility {
		if b > a {
			return b
		}
		return a
	}
	children := func(args ...expr.Expr) expr.Volatility {
		if intrinsicOnly {
			return expr.Immutable
		}
		v := expr.Immutable
		for _, a := range args {
			v = merge(v, plannedPhysicalVolatility(a, copyNextval))
		}
		return v
	}
	session := func(v expr.Volatility, dependent bool) expr.Volatility {
		if dependent {
			return merge(v, expr.Stable)
		}
		return v
	}
	switch n := node.(type) {
	case sequenceCallExpression:
		if copyNextval && n.name == "nextval" {
			return children(n.args...)
		}
		return n.Properties().Volatility
	case assignmentValueExpression:
		v := children(n.base, n.value)
		for target := n.target; target != nil; target = target.next {
			for _, sub := range target.node.subscripts {
				v = merge(v, children(sub.lower, sub.upper))
			}
			v = session(v, target.reapply.SessionDependent())
		}
		return v
	case rowExpression:
		return children(n.fields...)
	case fieldExpression:
		return children(n.base)
	case arrayExpression:
		return children(n.elements...)
	case subscriptExpression:
		var storage [13]expr.Expr
		return children(subscriptChildren(n, &storage)...)
	case domainValueExpression, literalExpression, columnExpression:
		return expr.Immutable
	case collationExpression:
		return children(n.child)
	case coercionExpression:
		return session(children(n.child), n.plan.SessionDependent())
	case rowComparisonExpression:
		v := expr.Immutable
		for _, pair := range n.comparisons {
			if intrinsicOnly {
				v = merge(v, plannedNodeVolatility(pair, false, true))
			} else {
				v = merge(v, plannedVolatility(pair))
			}
		}
		return v
	case scalarArrayComparisonExpression:
		v := children(n.left)
		if n.array != nil {
			v = merge(v, children(n.array))
		}
		v = merge(v, children(n.items...))
		return session(v, n.predicate.SessionDependent())
	case comparisonExpression:
		return session(children(n.left, n.right), n.predicate.SessionDependent())
	case selectedOperatorExpression:
		return children(n.provider)
	case arithmeticExpression:
		return session(children(n.left, n.right), n.provider.SessionDependent())
	case bitwiseExpression:
		return children(n.left, n.right)
	case likeExpression:
		return children(n.left, n.right)
	case seriesExpression:
		return children(n.args...)
	case subscriptsExpression:
		return children(n.args...)
	case unnestExpression:
		return children(n.args...)
	case nullTestExpression:
		return children(n.child)
	case truthTestExpression:
		return children(n.child)
	case logicalExpression:
		return children(n.left, n.right)
	case distinctExpression:
		return session(children(n.left, n.right), n.equality.SessionDependent())
	case conditionalExpression:
		return session(children(n.args...), n.comparison.SessionDependent())
	case aclValueExpression:
		return merge(children(n.arguments()...), n.baseProperties().Volatility)
	case objectAddressExpression:
		return merge(children(n.args[:]...), expr.Stable)
	case catalogUserNameExpression:
		return merge(children(n.arg), expr.Stable)
	case catalogForeignKeysExpression:
		return expr.Stable
	case transactionIdentityExpression:
		volatility := expr.Stable
		if n.kind == identityStatus || n.kind == identityMembers {
			volatility = expr.Volatile
		}
		if n.arg != nil {
			return merge(children(n.arg), volatility)
		}
		return volatility
	case commitTimestampExpression:
		return expr.Volatile
	case installedSoundexFunctionExpression:
		return children(n.Args...)
	case installedMetaphoneFunctionExpression:
		return children(n.Args...)
	case installedLevenshteinFunctionExpression:
		return children(n.Args...)
	case installedUUIDOsspFunctionExpression:
		return expr.Volatile
	case installedCryptoFunctionExpression:
		return merge(n.procedure.volatility, children(n.Args...))
	case installedFunctionExpression:
		return merge(n.procedure.volatility, children(n.Arg))
	case caseExpression:
		v := children(n.test, n.otherwise)
		for _, arm := range n.arms {
			v = merge(v, children(arm.condition, arm.result))
			if n.test != nil {
				v = session(v, arm.testPlan.SessionDependent() || arm.equality.SessionDependent())
			}
		}
		return v
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence.
		intrinsic, ok := n.(expr.Intrinsic)
		if !ok {
			return n.Properties().Volatility
		}
		return merge(children(n.Children()...), intrinsic.IntrinsicVolatility())
	default:
		return node.Properties().Volatility
	}
}
