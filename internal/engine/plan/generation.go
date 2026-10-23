package plan

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// GenerationImmutable admits a generation expression. Walk surviving
// providers, not conservative properties retained on CASE wrappers.
func GenerationImmutable(node expr.Expr) bool {
	if node == nil {
		return true
	}
	one := GenerationImmutable
	both := func(a, b expr.Expr) bool { return one(a) && one(b) }
	args := func(nodes []expr.Expr) bool {
		for _, child := range nodes {
			if !one(child) {
				return false
			}
		}
		return true
	}
	switch n := node.(type) {
	case RowExpr:
		return args(n.Fields)
	case FieldExpr:
		return one(n.Base)
	case ArrayExpr:
		return args(n.Elements)
	case SubscriptExpr:
		var storage [13]expr.Expr
		return args(SubscriptChildren(n, &storage))
	case LiteralExpr, ColumnExpr, TableOIDExpr:
		return true
	case CollationExpr:
		return one(n.Child)
	case CoercionExpr:
		return one(n.Child) && (n.Mode == sqltype.Assignment || !n.Plan.SessionDependent())
	case SelectedOperatorExpr:
		return one(n.Provider)
	case ArithmeticExpr:
		return !n.Provider.SessionDependent() && both(n.Left, n.Right)
	case BitwiseExpr:
		return both(n.Left, n.Right)
	case RowComparisonExpr:
		for _, pair := range n.Comparisons {
			if pair.Predicate.SessionDependent() || !both(pair.Left, pair.Right) {
				return false
			}
		}
		return true
	case ScalarArrayComparisonExpr:
		return !n.Predicate.SessionDependent() && one(n.Left) && (n.Array == nil || one(n.Array)) && args(n.Items)
	case ComparisonExpr:
		return !n.Predicate.SessionDependent() && both(n.Left, n.Right)
	case LogicalExpr:
		return both(n.Left, n.Right)
	case DistinctExpr:
		return !n.Equality.SessionDependent() && both(n.Left, n.Right)
	case NullTestExpr:
		return one(n.Child)
	case TruthTestExpr:
		return one(n.Child)
	case LikeExpr:
		return both(n.Left, n.Right)
	case ConditionalExpr:
		return !n.Comparison.SessionDependent() && args(n.Args)
	case CaseExpr:
		if !both(n.Test, n.Otherwise) {
			return false
		}
		for _, arm := range n.Arms {
			if !both(arm.Condition, arm.Result) || !one(arm.Test) || n.Test != nil && arm.Equality.SessionDependent() {
				return false
			}
		}
		return true
	case UnnestExpr:
		return false
	case ExtensionPathsExpr:
		return false
	case InstalledCubeExpr:
		return n.Procedure != nil && n.Procedure.Volatility == expr.Immutable && args(n.Args)
	case InstalledSoundexExpr:
		return args(n.Args)
	case InstalledMetaphoneExpr:
		return args(n.Args)
	case InstalledLevenshteinExpr:
		return args(n.Args)
	case InstalledUUIDOsspExpr:
		return n.Procedure != nil && n.Procedure.Volatility == expr.Immutable && args(n.Args)
	case InstalledCryptoExpr:
		return n.Procedure != nil && n.Procedure.Volatility == expr.Immutable && args(n.Args)
	case InstalledTextExpr:
		return n.Procedure != nil && n.Procedure.Volatility == expr.Immutable && args([]expr.Expr{n.Arg})
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence. A
		// provider is admitted by its own volatility and its children.
		if intrinsic, ok := n.(expr.Intrinsic); ok {
			return intrinsic.IntrinsicVolatility() == expr.Immutable && args(n.Children())
		}
	}
	return node.Properties().Volatility == expr.Immutable && !node.Properties().StageDependent && !node.Properties().ParameterDependent
}

// GenerationHasTableOID reports whether node reads the owner's tableoid.
func GenerationHasTableOID(node expr.Expr) bool {
	if node == nil {
		return false
	}
	one := GenerationHasTableOID
	both := func(a, b expr.Expr) bool { return one(a) || one(b) }
	args := func(nodes []expr.Expr) bool {
		for _, child := range nodes {
			if one(child) {
				return true
			}
		}
		return false
	}
	switch n := node.(type) {
	case RowExpr:
		return args(n.Fields)
	case FieldExpr:
		return one(n.Base)
	case ArrayExpr:
		return args(n.Elements)
	case SubscriptExpr:
		var storage [13]expr.Expr
		return args(SubscriptChildren(n, &storage))
	case TableOIDExpr:
		return true
	case CollationExpr:
		return one(n.Child)
	case CoercionExpr:
		return one(n.Child)
	case SelectedOperatorExpr:
		return one(n.Provider)
	case ArithmeticExpr:
		return both(n.Left, n.Right)
	case BitwiseExpr:
		return both(n.Left, n.Right)
	case RowComparisonExpr:
		for _, pair := range n.Comparisons {
			if both(pair.Left, pair.Right) {
				return true
			}
		}
		return false
	case ScalarArrayComparisonExpr:
		return one(n.Left) || n.Array != nil && one(n.Array) || args(n.Items)
	case ComparisonExpr:
		return both(n.Left, n.Right)
	case LogicalExpr:
		return both(n.Left, n.Right)
	case DistinctExpr:
		return both(n.Left, n.Right)
	case NullTestExpr:
		return one(n.Child)
	case TruthTestExpr:
		return one(n.Child)
	case LikeExpr:
		return both(n.Left, n.Right)
	case ConditionalExpr:
		return args(n.Args)
	case CaseExpr:
		if both(n.Test, n.Otherwise) {
			return true
		}
		for _, arm := range n.Arms {
			if both(arm.Condition, arm.Result) || one(arm.Test) {
				return true
			}
		}
		return false
	case ExtensionPathsExpr:
		return GenerationHasTableOID(n.Arg)
	case InstalledSoundexExpr:
		return args(n.Args)
	case InstalledMetaphoneExpr:
		return args(n.Args)
	case InstalledLevenshteinExpr:
		return args(n.Args)
	case InstalledUUIDOsspExpr:
		return args(n.Args)
	case InstalledCryptoExpr:
		return args(n.Args)
	case InstalledTextExpr:
		return args([]expr.Expr{n.Arg})
	case expr.Composite:
		return args(n.Children())
	default:
		return false
	}
}
