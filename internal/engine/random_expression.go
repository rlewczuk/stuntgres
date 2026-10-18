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

func randomLiteralNull(arg BoundExpr) bool {
	switch e := arg.(type) {
	case literalExpression:
		return e.value.IsNull()
	case collationExpression:
		return randomLiteralNull(e.child)
	case coercionExpression:
		return randomLiteralNull(e.child)
	}
	return false
}
