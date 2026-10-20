package engine

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) EvalGroup(e groupExpression) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	frame := e.frame
	if frame == 0 {
		frame = 1
	}
	for c != nil && (c.Scope != 1 || c.Frame != frame) {
		c = c.Outer
	}
	if c == nil || e.slot < 0 || e.slot >= len(c.Row) || c.Row[e.slot].Type() != e.Usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid group row frame")
	}
	return c.Row[e.slot], nil
}
