package engine

import "stuntgres/internal/engine/expr"

var _ expr.Context = (*EvalContext)(nil)

// evalContext recovers the engine evaluation context. A nil or foreign context
// yields nil, so the nodes' existing missing-context checks apply unchanged.
func evalContext(ctx expr.Context) *EvalContext {
	c, _ := ctx.(*EvalContext)
	return c
}
