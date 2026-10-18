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
