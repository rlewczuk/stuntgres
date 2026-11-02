package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// planningExecutionBarrier preserves demanded runtime diagnostics through
// enclosing computational frontiers. Node-local FoldNever alone does not imply
// this transitive barrier: relational providers still preflight their inputs.
// Unlike literal-only eligibility, this does not reject typed parameter inputs.
// Dormant declaration binders have no live host: their installed crypto calls
// remain symbolic, including beneath enclosing casts/arrays/composites. A live
// host supplies planning readiness, never a cached permission or policy decision.
func planningExecutionBarrier(c *Context, node expr.Expr) bool {
	barrier := false
	dormant := c.LiveHost() == nil
	plan.WalkExpression(node, func(child expr.Expr) {
		if _, ok := child.(expr.ExecutionDemand); ok {
			barrier = true
		}
		if _, crypto := child.(plan.InstalledCryptoExpr); crypto && dormant {
			barrier = true
		}
	})
	return barrier
}
