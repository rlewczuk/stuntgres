package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// RangeOperator is an infix scalar, not an invocable SQL function or SRF.
type RangeOperator struct {
	expr.Info
	Plan      sqltype.RangeOperatorPlan
	Args      [2]expr.Expr
	Namespace string
}

func (e RangeOperator) Eval(c expr.Context) (sqltype.Value, error) {
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
func NewRangeOperator(info expr.Info, op sqltype.RangeOperator, args [2]expr.Expr, namespace string) (RangeOperator, error) {
	plan, err := sqltype.BindRangeOperator(op, info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	return RangeOperator{info, plan, args, namespace}, err
}
func (e RangeOperator) Reconstruct(info expr.Info, args [2]expr.Expr) (RangeOperator, error) {
	return NewRangeOperator(info, e.Plan.Operation(), args, e.Namespace)
}
func (e RangeOperator) Validate() error {
	n, err := e.Reconstruct(e.Info, e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored range operator provider")
	}
	return nil
}
