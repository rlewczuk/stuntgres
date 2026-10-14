package engine

import "stuntgres/internal/sqltype"

// A query-macro NEW recipe uses the same typed path and assignment codec as the
// ordinary mutation executor; it does not execute a physical row callback.
type assignmentValueExpression struct {
	expressionInfo
	base, value BoundExpr
	target      *arrayAssignmentTarget
}

func (e assignmentValueExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	base, err := e.base.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.target.applyExpression(c, base, e.value)
}
