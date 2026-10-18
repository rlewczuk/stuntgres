package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type DatetimeJustify struct {
	expr.Info
	Plan sqltype.DatetimeJustifyPlan
	Arg  expr.Expr
}

func (e DatetimeJustify) Arguments() []expr.Expr { return []expr.Expr{e.Arg} }

func (e DatetimeJustify) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Arg) {
		return sqltype.Null(e.Usage.Type), nil
	}
	value, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(ctx, value)
}

func ResolveDatetimeJustify(visible, variadic bool, args []Argument, interval sqltype.Usage) TemporalSelection {
	if !visible || variadic || len(args) != 1 {
		return TemporalSelection{Resolution: Missing}
	}
	if d := args[0].Usage.Type; d != nil {
		if d.BaseType().ID() == sqltype.Time {
			return TemporalSelection{Unsupported: "time to interval conversion is unsupported"}
		}
		if _, err := sqltype.PlanCoercion(args[0].Usage, interval, sqltype.Implicit); err != nil {
			return TemporalSelection{Resolution: Missing}
		}
	}
	return TemporalSelection{Selected: sqltype.Interval}
}

func (e DatetimeJustify) Reconstruct(info expr.Info, arg expr.Expr, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeJustify, bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Arg, e.Plan = info, arg, plan
	return e, changed, nil
}
