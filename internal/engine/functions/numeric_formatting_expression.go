package functions

import (
	"context"
	"stuntgres/internal/sqltype"
)

type NumericFormattingContext interface {
	Context
	FunctionInput() sqltype.InputEnvironment
	FunctionOutput() sqltype.OutputEnvironment
}
type NumericFormatting[E Expression[C], C NumericFormattingContext, P any] struct {
	Info[P]
	Plan sqltype.NumericFormatPlan
	Args [2]E
}

func (e NumericFormatting[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
func (e NumericFormatting[E, C, P]) Reconstruct(info Info[P], args [2]E) (NumericFormatting[E, C, P], error) {
	plan, err := e.Plan.Retag(info.Usage, args[0].ResultUsage())
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}
func NewNumericFormatting[E Expression[C], C NumericFormattingContext, P any](ctx context.Context, info Info[P], name string, args [2]E, constant *string) (NumericFormatting[E, C, P], error) {
	plan, err := sqltype.BindNumericFormat(ctx, name, info.Usage, args[0].ResultUsage(), constant)
	return NumericFormatting[E, C, P]{info, plan, args}, err
}
func ResolveNumericFormatting[E any](visible, variadic bool, args []Argument[E]) Resolution {
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
func CoerceNumericFormattingArguments[E any, B ArgumentCoercer[E]](b B, arguments []Argument[E], source, text sqltype.Usage) (args [2]E, err error) {
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
func NumericFormattingIntrinsicRule() IntrinsicPropertyRule { return ExactStable }
