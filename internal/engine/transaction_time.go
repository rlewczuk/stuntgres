package engine

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) FunctionStatementTime() (time.Time, error) {
	host := c.liveHost()
	if host == nil {
		return time.Time{}, fmt.Errorf("stuntgres: missing statement time scope")
	}
	// A held cursor keeps its declaring statement's clock.
	if c.query != nil && c.query.source.authority != nil && c.query.source.authority.expression != nil {
		return c.query.source.authority.expression.statementStart, nil
	}
	return host.StatementStart(), nil
}
func (c *EvalContext) FunctionWallTime() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
func (c *EvalContext) FunctionTimeOfDayZone() *sqltype.Zone {
	return c.Input.Zone
}

func (c *EvalContext) FunctionSleepClock(usage sqltype.Usage) (sqltype.Value, error) {
	input := c.Input
	input.TransactionStart = c.FunctionWallTime()
	return sqltype.CurrentDateTime(usage, input)
}
func (c *EvalContext) FunctionSleepArgumentPresent(arg expr.Expr) bool { return arg != nil }

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
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case plan.ExtensionPathsExpr:
		return false
	case plan.ScalarArrayComparisonExpr:
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case plan.CoercionExpr, plan.CollationExpr, plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr, plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr, plan.ConditionalExpr, plan.CaseExpr:
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case expr.ConditionalClosure:
		return n.ClosedDefault(closedTransactionTimeDefault)
	case expr.ClosureTransparent:
		// Function nodes the closure looks through.
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	default:
		return p.Volatility <= expr.Stable
	}
}
