package plan

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// PLLocalExpr is a reached expression's immutable lexical input. It owns no
// frame pointer and cannot keep a closed activation alive. Values are immutable;
// the next reached expression binds against its own current slot/record shape.
type PLLocalExpr struct {
	expr.Info
	Value sqltype.Value
}

func (e PLLocalExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Value.Type() != e.Usage.Type || e.Usage.Type == nil || !e.Usage.Type.Concrete() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid PL lexical input")
	}
	return e.Value, nil
}

func (PLLocalExpr) Children() []expr.Expr { return nil }
func (e PLLocalExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 || info.Usage.Type != e.Value.Type() {
		return nil, fmt.Errorf("stuntgres: invalid PL lexical input reconstruction")
	}
	e.Info = info
	return e, nil
}
func (PLLocalExpr) PlanningFold() expr.Fold              { return expr.FoldNever }
func (PLLocalExpr) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
