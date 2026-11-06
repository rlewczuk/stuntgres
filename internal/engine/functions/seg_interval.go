package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// SegInterval is the reconstructible typed kernel; the plan wrapper owns the
// installed procedure identity, namespace rendering and runtime admission.
type SegInterval struct {
	expr.Info
	Plan sqltype.SegIntervalPlan
	Args [2]expr.Expr
}

func NewSegInterval(info expr.Info, op sqltype.SegIntervalOperation, args [2]expr.Expr) (SegInterval, error) {
	if missingArgument(args[:]) {
		return SegInterval{}, fmt.Errorf("stuntgres: missing seg interval operand")
	}
	p, err := sqltype.BindSegInterval(op, info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	return SegInterval{Info: info, Plan: p, Args: args}, err
}
func (e SegInterval) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e SegInterval) Children() []expr.Expr { return e.Args[:] }
func (e SegInterval) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("SegInterval", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(NewSegInterval(info, e.Plan.Operation(), [2]expr.Expr{children[0], children[1]}))
}
func (e SegInterval) Validate() error {
	n, err := NewSegInterval(e.Info, e.Plan.Operation(), e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored seg interval kernel")
	}
	return nil
}
func (e SegInterval) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e SegInterval) ValidateStoredProvider() error        { return e.Validate() }
func (e SegInterval) Render() (expr.Rendering, error) {
	spec := callRendering("SegInterval "+e.Plan.Operation().Routine(), e.Children(), e.Plan.Operation().Routine(), e.Children())
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e SegInterval) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(SegInterval)
	return ok && e.Plan == o.Plan && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e SegInterval) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e SegInterval) ClosureTransparent() {}
func (e SegInterval) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}

var _ expr.Composite = SegInterval{}
var _ expr.Renderable = SegInterval{}
