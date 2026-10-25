package core

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (h *statementHost) FunctionRandomOwner() (expr.RandomOwner, error) {
	scope := h.scope()
	if scope.Op == nil || scope.Op.Session == nil {
		return nil, fmt.Errorf("stuntgres: missing random session scope")
	}
	return &scope.Op.Session.Random, nil
}
