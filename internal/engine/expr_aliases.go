package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// Temporary aliases for the expression contract that moved to expr in
// ENGINE-SPLIT step 2b. Step 2d replaces their uses and removes them.
type (
	BoundExpr      = expr.Expr
	ExprProperties = expr.Properties
	ExprContext    = expr.Context
	Volatility     = expr.Volatility
	expressionInfo = expr.Info
)

const (
	Immutable = expr.Immutable
	Stable    = expr.Stable
	Volatile  = expr.Volatile
)

func boundExpressionInfo(node BoundExpr) expressionInfo { return expr.InfoOf(node) }

func mergeExpressionProperties(a, b ExprProperties) ExprProperties {
	return expr.MergeProperties(a, b)
}

func expressionError(code, message string, pos parser.Position) error {
	return expr.ErrorAt(code, message, pos)
}
