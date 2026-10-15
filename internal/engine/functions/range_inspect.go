package functions

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// RangeInspect evaluates strict scalar calls without exposing endpoint storage.
type RangeInspect[E Expression[C], C LiteralContext[E], P any] struct {
	Info[P]
	Plan sqltype.RangeInspectPlan
	Args []E
}

func (e RangeInspect[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
func (e RangeInspect[E, C, P]) Reconstruct(info Info[P], args []E) (RangeInspect[E, C, P], error) {
	replacement, err := NewRangeInspect[E, C](info, e.Plan.Operation(), args)
	if err != nil {
		return e, err
	}
	return replacement, nil
}
func (e RangeInspect[E, C, P]) Validate() error {
	replacement, err := NewRangeInspect[E, C](e.Info, e.Plan.Operation(), e.Args)
	if err != nil || replacement.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored range inspection provider")
	}
	return nil
}
func NewRangeInspect[E Expression[C], C LiteralContext[E], P any](info Info[P], operation sqltype.RangeInspectOperation, args []E) (RangeInspect[E, C, P], error) {
	var usages [2]sqltype.Usage
	if len(args) < 1 || len(args) > 2 {
		return RangeInspect[E, C, P]{}, fmt.Errorf("stuntgres: invalid range inspection arity")
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindRangeInspect(operation, info.Usage, usages[:len(args)]...)
	return RangeInspect[E, C, P]{info, plan, args}, err
}

func ResolveRangeInspect[E any](operation sqltype.RangeInspectOperation, visible bool, args []Argument[E]) (*sqltype.Descriptor, Resolution) {
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
func CoerceRangeInspectArguments[E any, B ArgumentCoercer[E]](b B, args []Argument[E], target sqltype.Usage, name string) ([]E, error) {
	result := make([]E, len(args))
	for i, arg := range args {
		value, err := b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
		if err != nil {
			return nil, err
		}
		result[i] = value
	}
	return result, nil
}
func RangeInspectIntrinsicRule() IntrinsicPropertyRule { return ExactImmutable }
