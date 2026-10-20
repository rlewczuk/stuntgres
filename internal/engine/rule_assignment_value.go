package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// A query-macro NEW recipe uses the same typed path and assignment codec as the
// ordinary mutation executor; it does not execute a physical row callback.
type assignmentValueExpression struct {
	expr.Info
	base, value expr.Expr
	target      *arrayAssignmentTarget
}

func (e assignmentValueExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	base, err := e.base.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.target.applyExpression(ctx, base, e.value)
}
