package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// RangeInspect evaluates strict scalar calls without exposing endpoint storage.
type RangeInspect struct {
	expr.Info
	Plan sqltype.RangeInspectPlan
	Args []expr.Expr
}

func (e RangeInspect) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values)
}
func (e RangeInspect) Reconstruct(info expr.Info, args []expr.Expr) (RangeInspect, error) {
	replacement, err := NewRangeInspect(info, e.Plan.Operation(), args)
	if err != nil {
		return e, err
	}
	return replacement, nil
}

// RangeInspect implements expr.Composite.
var _ expr.Composite = RangeInspect{}

func (e RangeInspect) Children() []expr.Expr { return e.Args }
func (e RangeInspect) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("RangeInspect", children, len(e.Args)); err != nil {
		return nil, err
	}
	if err := checkPresent("stuntgres: invalid remapped range inspection argument", children); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}
func (e RangeInspect) Validate() error {
	replacement, err := NewRangeInspect(e.Info, e.Plan.Operation(), e.Args)
	if err != nil || replacement.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored range inspection provider")
	}
	return nil
}
func NewRangeInspect(info expr.Info, operation sqltype.RangeInspectOperation, args []expr.Expr) (RangeInspect, error) {
	var usages [2]sqltype.Usage
	if len(args) < 1 || len(args) > 2 {
		return RangeInspect{}, fmt.Errorf("stuntgres: invalid range inspection arity")
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindRangeInspect(operation, info.Usage, usages[:len(args)]...)
	return RangeInspect{info, plan, args}, err
}

func ResolveRangeInspect(operation sqltype.RangeInspectOperation, visible bool, args []Argument) (*sqltype.Descriptor, Resolution) {
	if !visible || len(args) < 1 || len(args) > 2 || operation != sqltype.RangeMerge && len(args) != 1 {
		return nil, Missing
	}
	var selected *sqltype.Descriptor
	for _, arg := range args {
		if arg.Usage.Type == nil {
			continue
		}
		typ := arg.Usage.Type.BaseType()
		if !sqltype.IsRangeInspectType(typ) || selected != nil && selected != typ {
			return nil, Missing
		}
		selected = typ
	}
	if selected == nil {
		if operation == sqltype.RangeMerge || operation == sqltype.RangeConstruct {
			return nil, UnknownPolymorphic
		}
		return nil, Ambiguous
	}
	result := sqltype.RangeInspectResult(operation, selected)
	if result == nil {
		return nil, Missing
	}
	if operation == sqltype.RangeMerge {
		// Unary merge consumes multiranges; binary merge consumes one range family.
		if len(args) == 1 && result == selected || len(args) == 2 && result != selected {
			return nil, Missing
		}
	}
	return selected, Resolved
}
func CoerceRangeInspectArguments(b ArgumentCoercer, args []Argument, target sqltype.Usage, name string) ([]expr.Expr, error) {
	result := make([]expr.Expr, len(args))
	for i, arg := range args {
		value, err := b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
		if err != nil {
			return nil, err
		}
		result[i] = value
	}
	return result, nil
}
func RangeInspectIntrinsicRule() expr.IntrinsicPropertyRule { return expr.ExactImmutable }

func (e RangeInspect) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e RangeInspect) ValidateStoredProvider() error { return nil }
