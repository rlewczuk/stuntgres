package functions

import (
	"stuntgres/internal/sqltype"
)

type DatetimeJustifyContext[E any] interface {
	LiteralContext[E]
}

type DatetimeJustify[E Expression[C], C DatetimeJustifyContext[E], P any] struct {
	Info[P]
	Plan sqltype.DatetimeJustifyPlan
	Arg  E
}

func (e DatetimeJustify[E, C, P]) Arguments() []E { return []E{e.Arg} }

func (e DatetimeJustify[E, C, P]) Eval(c C) (sqltype.Value, error) {
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

func ResolveDatetimeJustify[E any](visible, variadic bool, args []Argument[E], interval sqltype.Usage) TemporalSelection {
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

func (e DatetimeJustify[E, C, P]) Reconstruct(info Info[P], arg E, retain func(*sqltype.Descriptor) *sqltype.Descriptor) (DatetimeJustify[E, C, P], bool, error) {
	plan, err := e.Plan.Retag(retain)
	if err != nil {
		return e, false, err
	}
	changed := plan != e.Plan
	e.Info, e.Arg, e.Plan = info, arg, plan
	return e, changed, nil
}
