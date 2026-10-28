package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Only a physical SQL constant NULL is decisive after complete analysis and
// child planning. Imported parameter values and row-owned NULLs are not planning
// facts; conservative row/stage dependence on an already-pruned literal remains.
func bitwiseConstantNull(node expr.Expr) bool {
	literal, ok := node.(plan.LiteralExpr)
	return ok && !literal.Property.ParameterDependent && literal.Value.IsNull()
}

// Opted-in network and pg_lsn signatures admit imported-parameter Execute planning.
// Conservative properties survive folding; availability is not SQL constness.
func OperatorExecutionConstant(node expr.Expr, c *Context) bool {
	if node == nil {
		return false
	}
	p := node.Properties()
	if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.DomainDependent || p.RuntimeDomainConversion {
		return false
	}
	switch n := node.(type) {
	case plan.LiteralExpr:
		return true
	case plan.ParameterExpr:
		return n.Number > 0 && n.Number <= len(c.Parameters)
	case plan.ColumnExpr, plan.GroupExpr, plan.AggregateExpr, plan.GroupingExpr, plan.SubqueryExpr:
		return false
	}
	found, constant := false, true
	plan.ExpressionChildren(node, func(child expr.Expr) {
		// Lexical enumeration includes absent optional operands (for example
		// unary operators); they contribute neither demand nor readiness.
		if child == nil {
			return
		}
		found = true
		constant = constant && OperatorExecutionConstant(child, c)
	})
	return found && constant
}
