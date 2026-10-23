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
	Args      [2]expr.Expr
	Namespace string
}

func NewGeometryMeasure(info expr.Info, op sqltype.GeometryMeasure, args [2]expr.Expr, namespace string) (GeometryMeasure, error) {
	if op.Name() == "" || missingArgument(args[:op.Arity()]) || op.Arity() == 1 && args[1] != nil {
		return GeometryMeasure{}, fmt.Errorf("stuntgres: invalid geometry measurement operands")
	}
	var usages [2]sqltype.Usage
	for i := range op.Arity() {
		usages[i] = args[i].ResultUsage()
	}
	p, err := sqltype.BindGeometryMeasure(op, info.Usage, usages)
	return GeometryMeasure{info, p, args, namespace}, err
}
func (e GeometryMeasure) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e GeometryMeasure) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Children() {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Children() {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return e.Plan.Evaluate(ctx, values)
}
func (e GeometryMeasure) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GeometryMeasure", children, len(e.Children())); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	copy(args[:], children)
	return rebuilt(NewGeometryMeasure(info, e.Plan.Operation(), args, e.Namespace))
}
func (e GeometryMeasure) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GeometryMeasure", children, len(e.Children())); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (e GeometryMeasure) Validate() error {
	n, err := NewGeometryMeasure(e.Info, e.Plan.Operation(), e.Args, e.Namespace)
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
	return expr.CaptureOperands{Inline: e.Args, Count: len(e.Children())}, true, nil
}
func (e GeometryMeasure) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(GeometryMeasure)
	if !ok || e.Plan != o.Plan || e.Namespace != o.Namespace {
		return false
	}
	for i, arg := range e.Children() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e GeometryMeasure) Render() (expr.Rendering, error) {
	spec := callRendering("GeometryMeasure "+e.Plan.Operation().Name()+" namespace "+e.Namespace, e.Children(), e.Plan.Operation().Name(), e.Children())
	transportArgs(spec.Args, expr.TransportLiteral)
	spec.Namespace = e.Namespace
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}

var _ expr.Composite = GeometryMeasure{}
var _ expr.Renderable = GeometryMeasure{}
