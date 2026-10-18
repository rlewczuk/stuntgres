package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type Enum struct {
	expr.Info
	Plan     sqltype.EnumSupportPlan
	Args     []expr.Expr
	EnumName string
}

func (e Enum) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e Enum) Reconstruct(info expr.Info, args []expr.Expr) (Enum, error) {
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

// Enum implements expr.Composite.
var _ expr.Composite = Enum{}

func (e Enum) Children() []expr.Expr { return e.Args }
func (e Enum) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Enum", children, len(e.Args)); err != nil {
		return nil, err
	}
	if err := checkPresent("stuntgres: invalid remapped enum argument", children); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}
func (e Enum) Validate() error {
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
func NewEnum(info expr.Info, name string, args []expr.Expr, enumName string) (Enum, error) {
	var usages [2]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindEnumSupport(name, info.Usage, usages[:len(args)]...)
	return Enum{info, plan, args, enumName}, err
}

type EnumSelection struct {
	Descriptor *sqltype.Descriptor
	Range      bool
}

func ResolveEnum(name string, visible bool, arguments []Argument) (EnumSelection, Resolution) {
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
func CoerceEnumArguments(b ArgumentCoercer, arguments []Argument, target sqltype.Usage, name string) ([]expr.Expr, error) {
	args := make([]expr.Expr, len(arguments))
	for i, argument := range arguments {
		arg, err := b.CoerceArgument(argument, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}
	return args, nil
}

func EnumIntrinsicRule() expr.IntrinsicPropertyRule { return expr.ExactStable }

func (e Enum) IntrinsicVolatility() expr.Volatility { return expr.Stable }

func (e Enum) ValidateStoredProvider() error { return nil }
