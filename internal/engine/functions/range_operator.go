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

// RangeOperator implements expr.Composite.
var _ expr.Composite = RangeOperator{}

func (e RangeOperator) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e RangeOperator) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("RangeOperator", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}
func (e RangeOperator) Validate() error {
	n, err := e.Reconstruct(e.Info, e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored range operator provider")
	}
	return nil
}

func (e RangeOperator) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e RangeOperator) ValidateStoredProvider() error { return e.Validate() }

var _ expr.Renderable = RangeOperator{}

// Render implements expr.Renderable.
func (e RangeOperator) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: "RangeOperator " + e.Plan.Name() + " namespace " + e.Namespace, Inputs: e.Args[:]}, nil
}
