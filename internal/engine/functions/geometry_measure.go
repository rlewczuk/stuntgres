package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// GeometryMeasure carries the selected typed plan and builtin routine identity.
type GeometryMeasure struct {
	expr.Info
	Plan      sqltype.GeometryMeasurePlan
	Arg       expr.Expr
	Namespace string
}

func NewGeometryMeasure(info expr.Info, op sqltype.GeometryMeasure, arg expr.Expr, namespace string) (GeometryMeasure, error) {
	if arg == nil {
		return GeometryMeasure{}, fmt.Errorf("stuntgres: missing geometry measurement operand")
	}
	p, err := sqltype.BindGeometryMeasure(op, info.Usage, arg.ResultUsage())
	return GeometryMeasure{info, p, arg, namespace}, err
}
func (e GeometryMeasure) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e GeometryMeasure) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Arg) {
		return sqltype.Null(e.Usage.Type), nil
	}
	v, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(ctx, v)
}
func (e GeometryMeasure) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GeometryMeasure", children, 1); err != nil {
		return nil, err
	}
	return rebuilt(NewGeometryMeasure(info, e.Plan.Operation(), children[0], e.Namespace))
}
func (e GeometryMeasure) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GeometryMeasure", children, 1); err != nil {
		return nil, err
	}
	e.Info, e.Arg = info, children[0]
	return e, nil
}
func (e GeometryMeasure) Validate() error {
	n, err := NewGeometryMeasure(e.Info, e.Plan.Operation(), e.Arg, e.Namespace)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored geometry measurement")
	}
	return nil
}
func (e GeometryMeasure) ValidateStoredProvider() error        { return e.Validate() }
func (e GeometryMeasure) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e GeometryMeasure) LooksLikeFunction() bool              { return true }
func (e GeometryMeasure) ClosureTransparent()                  {}
func (e GeometryMeasure) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: [2]expr.Expr{e.Arg}, Count: 1}, true, nil
}
func (e GeometryMeasure) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(GeometryMeasure)
	return ok && e.Plan == o.Plan && e.Namespace == o.Namespace && same(e.Arg, o.Arg)
}
func (e GeometryMeasure) Render() (expr.Rendering, error) {
	children := e.Children()
	spec := callRendering("GeometryMeasure "+e.Plan.Operation().Name()+" namespace "+e.Namespace, children, e.Plan.Operation().Name(), children)
	// Overloaded literal and NULL arguments must keep their selected formal.
	transportArgs(spec.Args, expr.TransportLiteral)
	spec.Namespace = e.Namespace
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}

var _ expr.Composite = GeometryMeasure{}
var _ expr.Renderable = GeometryMeasure{}
