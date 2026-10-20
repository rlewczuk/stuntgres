package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// A selected SQL routine executes inside the existing DEFAULT statement owner.
// Its body is neither evaluated nor traversed at definition time. Closure here
// concerns caller arguments only; the callee performs ordinary live query/ACL
// admission when the default is demanded. Unrelated volatile producers retain
// their existing, independently bounded default admission rules.
func closedSQLRoutineDefault(node expr.Expr) bool {
	valid, containsSQL := sqlRoutineDefaultClosure(node)
	return valid && containsSQL
}

// Keep the SQL extension distinct from the legacy whole-expression closures:
// transparent wrappers around nextval alone must not become newly admissible.
func sqlRoutineDefaultClosure(node expr.Expr) (valid, containsSQL bool) {
	if node == nil {
		return true, false
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false, false
	}
	if p.Volatility <= expr.Stable {
		return true, false
	}
	switch n := node.(type) {
	case plan.SQLRoutineExpr:
		if n.Procedure == nil || n.Procedure.SQL == nil || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != n.Procedure.Address() || n.Info.RoutineReference != n.Procedure || n.ValidateStoredProvider() != nil {
			return false, false
		}
		containsSQL = true
	case plan.CoercionExpr, plan.CollationExpr, plan.RowExpr, plan.FieldExpr, plan.ArrayExpr, plan.SubscriptExpr,
		plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr,
		plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr,
		plan.ConditionalExpr, plan.CaseExpr, plan.ScalarArrayComparisonExpr:
	case expr.ClosureTransparent:
	default:
		return closedSequenceDefault(node) || closedUUIDDefault(node) || closedTransactionTimeDefault(node) || closedPgcryptoDefault(node), false
	}
	valid = true
	plan.ExpressionChildren(node, func(child expr.Expr) {
		if valid {
			childValid, childSQL := sqlRoutineDefaultClosure(child)
			valid = childValid
			containsSQL = containsSQL || childSQL
		}
	})
	return valid, containsSQL
}
