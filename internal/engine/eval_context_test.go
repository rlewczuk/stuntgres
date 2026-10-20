package engine

import "stuntgres/internal/engine/expr"

// evalContext recovers the engine evaluation context in test probes. A nil or
// foreign context yields nil.
func evalContext(ctx expr.Context) *EvalContext {
	c, _ := ctx.(*EvalContext)
	return c
}
