package engine

var _ ExprContext = (*EvalContext)(nil)

// evalContext recovers the engine evaluation context. A nil or foreign context
// yields nil, so the nodes' existing missing-context checks apply unchanged.
func evalContext(ctx ExprContext) *EvalContext {
	c, _ := ctx.(*EvalContext)
	return c
}
