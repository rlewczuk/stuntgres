package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (h *statementHost) FunctionRandomOwner() (expr.RandomOwner, error) {
	scope := h.scope()
	if scope.op == nil || scope.op.session == nil {
		return nil, fmt.Errorf("stuntgres: missing random session scope")
	}
	return &scope.op.session.random, nil
}
