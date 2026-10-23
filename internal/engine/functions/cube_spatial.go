package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// CubeSpatial is the reconstructible typed kernel; the plan wrapper owns the
// installed procedure identity, namespace rendering and runtime admission.
type CubeSpatial struct {
	expr.Info
	Plan sqltype.CubeSpatialPlan
	Args [2]expr.Expr
}

func NewCubeSpatial(info expr.Info, op sqltype.CubeSpatialOperation, args [2]expr.Expr) (CubeSpatial, error) {
	if missingArgument(args[:]) {
		return CubeSpatial{}, fmt.Errorf("stuntgres: missing cube spatial operand")
	}
	p, err := sqltype.BindCubeSpatial(op, info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	return CubeSpatial{Info: info, Plan: p, Args: args}, err
}
func (e CubeSpatial) Eval(c expr.Context) (sqltype.Value, error) {
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
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return e.Plan.Evaluate(ctx, values)
}
func (e CubeSpatial) Children() []expr.Expr { return e.Args[:] }
func (e CubeSpatial) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("CubeSpatial", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(NewCubeSpatial(info, e.Plan.Operation(), [2]expr.Expr{children[0], children[1]}))
}
func (e CubeSpatial) Validate() error {
	n, err := NewCubeSpatial(e.Info, e.Plan.Operation(), e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored cube spatial kernel")
	}
	return nil
}
func (e CubeSpatial) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e CubeSpatial) ValidateStoredProvider() error        { return e.Validate() }
func (e CubeSpatial) Render() (expr.Rendering, error) {
	spec := callRendering("CubeSpatial "+e.Plan.Operation().Routine(), e.Children(), e.Plan.Operation().Routine(), e.Children())
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e CubeSpatial) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(CubeSpatial)
	return ok && e.Plan == o.Plan && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e CubeSpatial) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e CubeSpatial) ClosureTransparent() {}
func (e CubeSpatial) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}

var _ expr.Composite = CubeSpatial{}
var _ expr.Renderable = CubeSpatial{}
