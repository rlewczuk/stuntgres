package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
)

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
	case functions.SequenceCall:
		if copyNextval && n.Name == "nextval" {
			return children(n.Args...)
		}
		return n.Properties().Volatility
	case plan.AssignmentValueExpr:
		v := children(n.Base, n.Value)
		for target := n.Target; target != nil; target = target.Next {
			for _, sub := range target.Node.Subscripts {
				v = merge(v, children(sub.Lower, sub.Upper))
			}
			v = session(v, target.Reapply.SessionDependent())
		}
		return v
	case plan.RowExpr:
		return children(n.Fields...)
	case plan.FieldExpr:
		return children(n.Base)
	case plan.ArrayExpr:
		return children(n.Elements...)
	case plan.SubscriptExpr:
		var storage [13]expr.Expr
		return children(subscriptChildren(n, &storage)...)
	case plan.DomainValueExpr, plan.LiteralExpr, plan.ColumnExpr:
		return expr.Immutable
	case plan.CollationExpr:
		return children(n.Child)
	case plan.CoercionExpr:
		return session(children(n.Child), n.Plan.SessionDependent())
	case plan.RowComparisonExpr:
		v := expr.Immutable
		for _, pair := range n.Comparisons {
			if intrinsicOnly {
				v = merge(v, plannedNodeVolatility(pair, false, true))
			} else {
				v = merge(v, plannedVolatility(pair))
			}
		}
		return v
	case plan.ScalarArrayComparisonExpr:
		v := children(n.Left)
		if n.Array != nil {
			v = merge(v, children(n.Array))
		}
		v = merge(v, children(n.Items...))
		return session(v, n.Predicate.SessionDependent())
	case plan.ComparisonExpr:
		return session(children(n.Left, n.Right), n.Predicate.SessionDependent())
	case plan.SelectedOperatorExpr:
		return children(n.Provider)
	case plan.ArithmeticExpr:
		return session(children(n.Left, n.Right), n.Provider.SessionDependent())
	case plan.BitwiseExpr:
		return children(n.Left, n.Right)
	case plan.LikeExpr:
		return children(n.Left, n.Right)
	case plan.NullTestExpr:
		return children(n.Child)
	case plan.TruthTestExpr:
		return children(n.Child)
	case plan.LogicalExpr:
		return children(n.Left, n.Right)
	case plan.DistinctExpr:
		return session(children(n.Left, n.Right), n.Equality.SessionDependent())
	case plan.ConditionalExpr:
		return session(children(n.Args...), n.Comparison.SessionDependent())
	case extensionPathsExpression:
		return merge(children(n.arg), expr.Stable)
	case installedSoundexFunctionExpression:
		return children(n.Args...)
	case installedMetaphoneFunctionExpression:
		return children(n.Args...)
	case installedLevenshteinFunctionExpression:
		return children(n.Args...)
	case installedUUIDOsspFunctionExpression:
		return n.procedure.Volatility
	case installedCryptoFunctionExpression:
		return merge(n.procedure.Volatility, children(n.Args...))
	case installedFunctionExpression:
		return merge(n.procedure.Volatility, children(n.Arg))
	case plan.CaseExpr:
		v := children(n.Test, n.Otherwise)
		for _, arm := range n.Arms {
			v = merge(v, children(arm.Condition, arm.Result))
			if n.Test != nil {
				v = session(v, arm.TestPlan.SessionDependent() || arm.Equality.SessionDependent())
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
