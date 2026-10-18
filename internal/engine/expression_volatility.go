package engine

import "stuntgres/internal/engine/functions"

// plannedVolatility inspects the physical surviving tree. Analysis properties
// deliberately retain dependence from eliminated CASE/COALESCE arms.
func plannedVolatility(node BoundExpr) Volatility {
	return plannedPhysicalVolatility(node, false)
}

// COPY's supported nextval defaults are batch-safe, but their argument trees
// are still inspected. Other consumers retain ordinary physical volatility.
func plannedPhysicalVolatility(node BoundExpr, copyNextval bool) Volatility {
	return plannedNodeVolatility(node, copyNextval, false)
}

// Use the same provider/signature authority without walking operand recipes.
func plannedIntrinsicVolatility(node BoundExpr) Volatility {
	return plannedNodeVolatility(node, false, true)
}

func plannedNodeVolatility(node BoundExpr, copyNextval, intrinsicOnly bool) Volatility {
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
		if intrinsicOnly {
			return Immutable
		}
		v := Immutable
		for _, a := range args {
			v = merge(v, plannedPhysicalVolatility(a, copyNextval))
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
		var storage [13]BoundExpr
		return children(subscriptChildren(n, &storage)...)
	case domainValueExpression, literalExpression, columnExpression:
		return Immutable
	case collationExpression:
		return children(n.child)
	case coercionExpression:
		return session(children(n.child), n.plan.SessionDependent())
	case rowComparisonExpression:
		v := Immutable
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
	case functions.LikeEscape[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Pattern, n.Escape)
	case functions.Posix[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Left, n.Right)
	case functions.Similar[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Left, n.Right)
	case functions.SimilarEscape[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Pattern, n.Escape)
	case functions.PosixSubstring[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args[:]...)
	case functions.SimilarSubstring[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args[:]...)
	case functions.Regexp[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case functions.RegexpSet[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case functions.JSONKeys[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case functions.JSONBRecord[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Args...), true)
	case functions.JSONBElements[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case functions.JSONRecord[BoundExpr, ExprContext, ExprProperties]:
		return max(Stable, children(n.Args...))
	case functions.JSONEach[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case functions.JSONElements[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
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
	case functions.Binary[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Args...), !n.Plan.Immutable())
	case functions.DatetimeJustify[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arg)
	case functions.DatetimeExtract[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Arguments()...), n.Plan.SessionDependent())
	case functions.DatetimeOverlaps[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Arguments()...), n.Plan.SessionDependent())
	case functions.DatetimeBin[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arguments()...)
	case functions.DatetimeTrunc[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Arguments()...), n.Plan.SessionDependent())
	case functions.DatetimeZoneCalendar[BoundExpr, ExprContext, ExprProperties]:
		v := Immutable
		if n.IntrinsicStable() {
			v = Stable
		}
		return merge(children(n.Arguments()...), v)
	case functions.DatetimeAgeFinite[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Arguments()...), n.Plan.SessionDependent())
	case functions.JSONStripNulls[BoundExpr, ExprContext, ExprProperties]:
		return merge(children(n.Arguments()...), Stable)
	case functions.JSONExtract[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arguments()...)
	case functions.JSONBPathQueryArray[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arguments()...)
	case functions.JSONBPathExists[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arguments()...)
	case functions.JSONBPathMatch[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arguments()...)
	case functions.JSONBPathQueryFirst[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arguments()...)
	case functions.JSONBPathQuery[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arguments()...)
	case functions.JSONBSet[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arguments()...)
	case functions.DatetimeMake[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Arguments()...), n.Plan.SessionDependent())
	case functions.DatetimeFormatting[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Args[:]...), true)
	case functions.NumericFormatting[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Args[:]...), true)
	case functions.String[BoundExpr, ExprContext, ExprProperties]:
		return session(children(n.Args...), n.Plan.SessionDependent())
	case functions.Timezone[BoundExpr, ExprContext, ExprProperties]:
		v := Immutable
		if n.IntrinsicStable() {
			v = Stable
		}
		return merge(children(n.Value, n.Zone), v)
	case functions.ArrayRandom[BoundExpr, ExprContext, ExprProperties]:
		return merge(children(n.Args...), Volatile)
	case functions.ArrayTransform[BoundExpr, ExprContext, ExprProperties]:
		v := children(n.Args...)
		if n.IntrinsicStable() {
			v = merge(v, Stable)
		}
		return v
	case functions.ArrayInspect[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case functions.ArraySearch[BoundExpr, ExprContext, ExprProperties]:
		v := children(n.Args...)
		if n.Plan.SessionDependent() {
			v = merge(v, Stable)
		}
		return v
	case aclValueExpression:
		return merge(children(n.arguments()...), n.baseProperties().Volatility)
	case objectAddressExpression:
		return merge(children(n.args[:]...), Stable)
	case catalogUserNameExpression:
		return merge(children(n.arg), Stable)
	case catalogForeignKeysExpression:
		return Stable
	case transactionIdentityExpression:
		volatility := Stable
		if n.kind == identityStatus || n.kind == identityMembers {
			volatility = Volatile
		}
		if n.arg != nil {
			return merge(children(n.arg), volatility)
		}
		return volatility
	case commitTimestampExpression:
		return Volatile
	case functions.UUIDExtract[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Arg)
	case functions.NetworkFormat[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args[:n.Kind.Arity()]...)
	case functions.RangeOperator[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args[:]...)
	case functions.RangeInspect[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case functions.Enum[BoundExpr, ExprContext, ExprProperties]:
		return merge(children(n.Args...), Stable)
	case functions.Math[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case functions.NullCount[BoundExpr, ExprContext, ExprProperties]:
		return children(n.Args...)
	case installedSoundexFunctionExpression:
		return children(n.Args...)
	case installedMetaphoneFunctionExpression:
		return children(n.Args...)
	case installedLevenshteinFunctionExpression:
		return children(n.Args...)
	case installedUUIDOsspFunctionExpression:
		return Volatile
	case installedCryptoFunctionExpression:
		return merge(n.procedure.volatility, children(n.Args...))
	case installedFunctionExpression:
		return merge(n.procedure.volatility, children(n.arg))
	case sqlJSONExpression:
		return merge(n.properties.Volatility, children(n.args...))
	case jsonPredicateExpression:
		return children(n.child)
	case functions.JSONCreate[BoundExpr, ExprContext, ExprProperties]:
		return merge(Stable, children(n.Args...))
	case functions.StringVariadic[BoundExpr, ExprContext, ExprProperties, quoteOrigin]:
		intrinsic := Immutable
		if n.IntrinsicStable() {
			intrinsic = Stable
		}
		return merge(intrinsic, children(n.Args...))
	case functions.Format[BoundExpr, ExprContext, ExprProperties]:
		return merge(Stable, children(n.Args...))
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
