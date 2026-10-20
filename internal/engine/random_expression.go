package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func (c *EvalContext) FunctionRandomOwner() (expr.RandomOwner, error) {
	host := c.liveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing random session scope")
	}
	return host.FunctionRandomOwner()
}

func (h *statementHost) FunctionRandomOwner() (expr.RandomOwner, error) {
	scope := h.scope()
	if scope.op == nil || scope.op.session == nil {
		return nil, fmt.Errorf("stuntgres: missing random session scope")
	}
	return &scope.op.session.random, nil
}

func randomLiteralNull(arg expr.Expr) bool {
	switch e := arg.(type) {
	case plan.LiteralExpr:
		return e.Value.IsNull()
	case plan.CollationExpr:
		return randomLiteralNull(e.Child)
	case plan.CoercionExpr:
		return randomLiteralNull(e.Child)
	}
	return false
}
