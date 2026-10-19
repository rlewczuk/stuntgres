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

// DatetimeJustify implements expr.Composite. Rebuild keeps the retained plan
// descriptors.
var _ expr.Composite = DatetimeJustify{}

func (e DatetimeJustify) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e DatetimeJustify) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("DatetimeJustify", children, 1); err != nil {
		return nil, err
	}
	node, _, err := e.Reconstruct(info, children[0], keepDescriptor)
	return rebuilt(node, err)
}

func (e DatetimeJustify) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e DatetimeJustify) StrictNull() expr.Strict { return expr.StrictAfter }

func (e DatetimeJustify) ValidateStoredProvider() error { return nil }

// Retag rebinds the plan's retained descriptors; a plan change counts as a
// change.
func (e DatetimeJustify) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	node, planChanged, err := e.Reconstruct(info, children[0], env.Type)
	return retagged(node, changed || planChanged, err)
}

var _ expr.Renderable = DatetimeJustify{}

// Render implements expr.Renderable.
func (e DatetimeJustify) Render() (expr.Rendering, error) {
	return callRendering("Datetime "+e.Plan.Name(), e.Arguments(), e.Plan.Name(), e.Arguments()), nil
}

// SameGroup implements expr.GroupIdentity.
func (e DatetimeJustify) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(DatetimeJustify)
	return ok && e.Plan.Name() == o.Plan.Name() && same(e.Arg, o.Arg)
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e DatetimeJustify) LooksLikeFunction() bool { return true }
