package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type NumericFormatting struct {
	expr.Info
	Plan sqltype.NumericFormatPlan
	Args [2]expr.Expr
}

func (e NumericFormatting) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values[0], values[1], c.FunctionInput(), c.FunctionOutput())
}
func (e NumericFormatting) Reconstruct(info expr.Info, args [2]expr.Expr) (NumericFormatting, error) {
	plan, err := e.Plan.Retag(info.Usage, args[0].ResultUsage())
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// NumericFormatting implements expr.Composite.
var _ expr.Composite = NumericFormatting{}

func (e NumericFormatting) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e NumericFormatting) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("NumericFormatting", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}
func NewNumericFormatting(ctx context.Context, info expr.Info, name string, args [2]expr.Expr, constant *string) (NumericFormatting, error) {
	plan, err := sqltype.BindNumericFormat(ctx, name, info.Usage, args[0].ResultUsage(), constant)
	return NumericFormatting{info, plan, args}, err
}
func ResolveNumericFormatting(visible, variadic bool, args []Argument) Resolution {
	if !visible || len(args) != 2 || variadic {
		return Missing
	}
	for _, argument := range args {
		if d := argument.Usage.Type; d != nil && !sqltype.StringTextInput(d.BaseType().ID()) {
			return Missing
		}
	}
	return Resolved
}
func CoerceNumericFormattingArguments(b ArgumentCoercer, arguments []Argument, source, text sqltype.Usage) (args [2]expr.Expr, err error) {
	for i, argument := range arguments {
		target := text
		if i == 0 {
			target = source
		}
		args[i], err = b.CoerceArgument(argument, CoercionRequest{Intent: TextFormalArgument, Target: target})
		if err != nil {
			return args, err
		}
	}
	return args, nil
}
func NumericFormattingResult(name string) sqltype.ID {
	if name == "to_number" {
		return sqltype.Numeric
	}
	return sqltype.Text
}
func NumericFormattingIntrinsicRule() expr.IntrinsicPropertyRule { return expr.ExactStable }

func (e NumericFormatting) IntrinsicVolatility() expr.Volatility { return expr.Stable }

func (e NumericFormatting) ValidateStoredProvider() error { return nil }

var _ expr.Renderable = NumericFormatting{}

// Render implements expr.Renderable.
func (e NumericFormatting) Render() (expr.Rendering, error) {
	return callRendering("NumericFormatting "+e.Plan.Name(), e.Args[:], e.Plan.Name(), e.Args[:]), nil
}

// SameGroup implements expr.GroupIdentity.
func (e NumericFormatting) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(NumericFormatting)
	return ok && e.Plan.Name() == o.Plan.Name() && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}

// SpecializeTemplate implements expr.ConstantTemplate: a literal template
// argument selects its parsed format once.
func (e NumericFormatting) SpecializeTemplate(ctx context.Context, literal func(expr.Expr) (sqltype.Value, bool)) (expr.Expr, bool, error) {
	template, ok := literal(e.Args[1])
	if !ok {
		return e, false, nil
	}
	plan, changed, err := e.Plan.WithConstantTemplate(ctx, template)
	if err != nil || !changed {
		return e, false, err
	}
	e.Plan = plan
	return e, true, nil
}

// Substitute implements expr.Substitutable.
func (e NumericFormatting) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("NumericFormatting", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// ClosureTransparent implements expr.ClosureTransparent.
func (e NumericFormatting) ClosureTransparent() {}

func (e NumericFormatting) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: len(e.Args)}, true, nil
}
