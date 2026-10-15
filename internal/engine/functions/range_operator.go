package functions

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// RangeOperator is an infix scalar, not an invocable SQL function or SRF.
type RangeOperator[E Expression[C], C LiteralContext[E], P any] struct {
	Info[P]
	Plan      sqltype.RangeOperatorPlan
	Args      [2]E
	Namespace string
}

func (e RangeOperator[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return e.Plan.Evaluate(ctx, values)
}
func NewRangeOperator[E Expression[C], C LiteralContext[E], P any](info Info[P], op sqltype.RangeOperator, args [2]E, namespace string) (RangeOperator[E, C, P], error) {
	plan, err := sqltype.BindRangeOperator(op, info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	return RangeOperator[E, C, P]{info, plan, args, namespace}, err
}
func (e RangeOperator[E, C, P]) Reconstruct(info Info[P], args [2]E) (RangeOperator[E, C, P], error) {
	return NewRangeOperator[E, C](info, e.Plan.Operation(), args, e.Namespace)
}
func (e RangeOperator[E, C, P]) Validate() error {
	n, err := e.Reconstruct(e.Info, e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored range operator provider")
	}
	return nil
}
