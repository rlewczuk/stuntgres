package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
)

// Use the same provider/signature authority without walking operand recipes.
func plannedIntrinsicVolatility(node expr.Expr) expr.Volatility {
	return exec.PlannedNodeVolatility(node, false, true)
}
