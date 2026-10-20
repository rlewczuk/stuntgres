package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
)

// Only selected time leaves may introduce volatility into this closed default.
// Other volatile producers retain their separately owned admission rules.
func closedTransactionTimeDefault(node expr.Expr) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	switch n := node.(type) {
	case functions.TransactionTime:
		return n.Validate(expr.PropertiesMatch(n.Property, n.Kind.IntrinsicRule())) == nil
	case functions.DatetimeSleep:
		return n.Validate(expr.PropertiesMatch(n.Property, functions.DatetimeSleepIntrinsicRule()), n.Arg != nil) && closedTransactionTimeDefault(n.Arg)
	case plan.RowExpr, plan.FieldExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case plan.ExtensionPathsExpr:
		return false
	case plan.ScalarArrayComparisonExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case plan.CoercionExpr, plan.CollationExpr, plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr, plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr, plan.ConditionalExpr, plan.CaseExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case expr.ConditionalClosure:
		return n.ClosedDefault(closedTransactionTimeDefault)
	case expr.ClosureTransparent:
		// Function nodes the closure looks through.
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	default:
		return p.Volatility <= expr.Stable
	}
}
