package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// GeometryConversion retains the selected formal signature and routine Info.
// Spelling alone never authorizes conversion execution or stored replay.
type GeometryConversion struct {
	expr.Info
	Plan      sqltype.GeometryConversionPlan
	Args      [2]expr.Expr
	Namespace string
}

func NewGeometryConversion(info expr.Info, op sqltype.GeometryConversion, args [2]expr.Expr, namespace string) (GeometryConversion, error) {
	if op.Name() == "" || missingArgument(args[:op.Arity()]) || op.Arity() == 1 && args[1] != nil {
		return GeometryConversion{}, fmt.Errorf("stuntgres: invalid geometry conversion operands")
	}
	var usages [2]sqltype.Usage
	for i := range op.Arity() {
		usages[i] = args[i].ResultUsage()
	}
	p, err := sqltype.BindGeometryConversion(op, info.Usage, usages)
	return GeometryConversion{info, p, args, namespace}, err
}
func (e GeometryConversion) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e GeometryConversion) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e GeometryConversion) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GeometryConversion", children, len(e.Children())); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	copy(args[:], children)
	return rebuilt(NewGeometryConversion(info, e.Plan.Operation(), args, e.Namespace))
}
func (e GeometryConversion) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("GeometryConversion", children, len(e.Children())); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (e GeometryConversion) Validate() error {
	n, err := NewGeometryConversion(e.Info, e.Plan.Operation(), e.Args, e.Namespace)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored geometry conversion")
	}
	return nil
}
func (e GeometryConversion) ValidateStoredProvider() error        { return e.Validate() }
func (e GeometryConversion) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e GeometryConversion) LooksLikeFunction() bool              { return true }
func (e GeometryConversion) ClosureTransparent()                  {}
func (e GeometryConversion) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: len(e.Children())}, true, nil
}
func (e GeometryConversion) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(GeometryConversion)
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
func (e GeometryConversion) Render() (expr.Rendering, error) {
	spec := callRendering("GeometryConversion "+e.Plan.Operation().Name()+" namespace "+e.Namespace, e.Children(), e.Plan.Operation().Name(), e.Children())
	// Quoted geometry and NULL literals must retain their selected formal type;
	// otherwise replay can select a typename input cast or another overload.
	transportArgs(spec.Args, expr.TransportLiteral)
	spec.Namespace = e.Namespace
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}

var _ expr.Composite = GeometryConversion{}
var _ expr.Renderable = GeometryConversion{}
