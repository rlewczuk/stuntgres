package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// planningExecutionBarrier preserves demanded runtime diagnostics through
// enclosing computational frontiers. Node-local FoldNever alone does not imply
// this transitive barrier: relational providers still preflight their inputs.
// Unlike literal-only eligibility, this does not reject typed parameter inputs.
func planningExecutionBarrier(node expr.Expr) bool {
	barrier := false
	plan.WalkExpression(node, func(child expr.Expr) {
		if _, ok := child.(expr.ExecutionDemand); ok {
			barrier = true
		}
	})
	return barrier
}
