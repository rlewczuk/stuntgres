package engine

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) FunctionStatementTime() (time.Time, error) {
	scope := c.liveScope()
	if scope == nil {
		return time.Time{}, fmt.Errorf("stuntgres: missing statement time scope")
	}
	stamp := scope.statementStart
	if c.query != nil && c.query.source.authority != nil && c.query.source.authority.expression != nil {
		stamp = c.query.source.authority.expression.statementStart
	}
	return stamp, nil
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
	case rowExpression, fieldExpression:
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case catalogUserNameExpression:
		return closedTransactionTimeDefault(n.arg)
	case catalogForeignKeysExpression, extensionPathsExpression:
		return false
	case transactionIdentityExpression:
		return n.arg == nil || closedTransactionTimeDefault(n.arg)
	case commitTimestampExpression:
		return n.arg == nil || closedTransactionTimeDefault(n.arg)
	case scalarArrayComparisonExpression:
		valid := true
		boundExpressionChildren(node, func(child expr.Expr) { valid = valid && closedTransactionTimeDefault(child) })
		return valid
	case coercionExpression, collationExpression, comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression, nullTestExpression, truthTestExpression, logicalExpression, distinctExpression, conditionalExpression, caseExpression:
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
