package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// A query-macro NEW recipe uses the same typed path and assignment codec as the
// ordinary mutation executor; it does not execute a physical row callback.
type assignmentValueExpression struct {
	expr.Info
	Base, Value expr.Expr
	Target      *arrayAssignmentTarget
}

func (e assignmentValueExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	base, err := e.Base.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Target.ApplyExpression(ctx, base, e.Value)
}
