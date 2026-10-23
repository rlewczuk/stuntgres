package exec

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) CastArrayElement(usage sqltype.Usage) (sqltype.Value, error) {
	if c.castElement.Type() != usage.Type {
		return sqltype.Value{}, fmt.Errorf("invalid array cast element frame")
	}
	return c.castElement, nil
}
func (c *Context) EvalArrayCast(n plan.ArrayCastExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	source, err := n.Child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	previous := c.castElement
	defer func() { c.castElement = previous }()
	return source.MapArray(n.Usage.Type, func(element sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
		c.castElement = element
		return n.Element.Eval(c)
	})
}

func (c *Context) CaseTestValue(usage sqltype.Usage) (sqltype.Value, error) {
	if c.caseTest.Type() != usage.Type {
		return sqltype.Value{}, fmt.Errorf("invalid CASE test frame")
	}
	return c.caseTest, nil
}

func (c *Context) EvalCaseTest(template expr.Expr, value sqltype.Value) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	previous := c.caseTest
	c.caseTest = value
	defer func() { c.caseTest = previous }()
	return template.Eval(c)
}
