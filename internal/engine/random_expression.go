package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (c *EvalContext) FunctionRandomOwner() (expr.RandomOwner, error) {
	if c.liveScope() == nil || c.liveScope().op == nil || c.liveScope().op.session == nil {
		return nil, fmt.Errorf("stuntgres: missing random session scope")
	}
	return &c.liveScope().op.session.random, nil
}

func randomLiteralNull(arg expr.Expr) bool {
	switch e := arg.(type) {
	case literalExpression:
		return e.Value.IsNull()
	case collationExpression:
		return randomLiteralNull(e.Child)
	case coercionExpression:
		return randomLiteralNull(e.Child)
	}
	return false
}
