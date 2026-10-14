package functions

import (
	"fmt"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type EnumContext interface {
	Context
	FunctionInput() sqltype.InputEnvironment
}
type Enum[E Expression[C], C EnumContext, P any] struct {
	Info[P]
	Plan     sqltype.EnumSupportPlan
	Args     []E
	EnumName string
}

func (e Enum[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
	input := c.FunctionInput()
	value, err := e.Plan.Evaluate(ctx, values[0], values[1], input)
	if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "55000" {
		name := e.EnumName
		if input.TypeDiagnosticName != nil {
			if current := input.TypeDiagnosticName(e.Plan.Enum().ID()); current != "" {
				name = current
			}
		}
		copy := *diagnostic
		copy.Message = fmt.Sprintf("enum %s contains no values", name)
		return sqltype.Value{}, &copy
	}
	return value, err
}
func (e Enum[E, C, P]) Reconstruct(info Info[P], args []E) (Enum[E, C, P], error) {
	var usages [2]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindEnumSupport(e.Plan.Name(), info.Usage, usages[:len(args)]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}
func (e Enum[E, C, P]) Validate() error {
	if len(e.Args) < 1 || len(e.Args) > 2 {
		return fmt.Errorf("stuntgres: invalid stored enum arity")
	}
	var usages [2]sqltype.Usage
	for i, arg := range e.Args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindEnumSupport(e.Plan.Name(), e.Usage, usages[:len(e.Args)]...)
	if err != nil || plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored enum provider")
	}
	return nil
}
func NewEnum[E Expression[C], C EnumContext, P any](info Info[P], name string, args []E, enumName string) (Enum[E, C, P], error) {
	var usages [2]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindEnumSupport(name, info.Usage, usages[:len(args)]...)
	return Enum[E, C, P]{info, plan, args, enumName}, err
}

type EnumSelection struct {
	Descriptor *sqltype.Descriptor
	Range      bool
}

func ResolveEnum[E any](name string, visible bool, arguments []Argument[E]) (EnumSelection, Resolution) {
	var selected *sqltype.Descriptor
	valid := visible && (len(arguments) == 1 || name == "enum_range" && len(arguments) == 2)
	for _, argument := range arguments {
		if typ := argument.Usage.Type; typ != nil {
			if !typ.IsEnum() || selected != nil && selected != typ {
				valid = false
			} else {
				selected = typ
			}
		}
	}
	if !valid || selected == nil {
		return EnumSelection{}, Missing
	}
	return EnumSelection{Descriptor: selected, Range: name == "enum_range"}, Resolved
}
func CoerceEnumArguments[E any, B ArgumentCoercer[E]](b B, arguments []Argument[E], target sqltype.Usage, name string) ([]E, error) {
	args := make([]E, len(arguments))
	for i, argument := range arguments {
		arg, err := b.CoerceArgument(argument, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}
	return args, nil
}

func EnumIntrinsicRule() IntrinsicPropertyRule { return ExactStable }
