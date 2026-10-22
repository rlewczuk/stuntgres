package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Polygon retains selected routine authority independently of call syntax.
type Polygon struct {
	expr.Info
	Plan      sqltype.PolygonPlan
	Args      [2]expr.Expr
	Namespace string
	Function  bool
}

func NewPolygon(info expr.Info, op sqltype.PolygonOperator, args [2]expr.Expr, namespace string, function bool) (Polygon, error) {
	if missingArgument(args[:]) {
		return Polygon{}, fmt.Errorf("stuntgres: invalid polygon operands")
	}
	p, err := sqltype.BindPolygon(op, info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	return Polygon{info, p, args, namespace, function}, err
}
func (e Polygon) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e Polygon) Children() []expr.Expr { return e.Args[:] }
func (e Polygon) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Polygon", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(NewPolygon(info, e.Plan.Operation(), [2]expr.Expr{children[0], children[1]}, e.Namespace, e.Function))
}
func (e Polygon) Validate() error {
	n, err := NewPolygon(e.Info, e.Plan.Operation(), e.Args, e.Namespace, e.Function)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored polygon provider")
	}
	return nil
}
func (e Polygon) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e Polygon) ValidateStoredProvider() error        { return e.Validate() }
func (e Polygon) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	name := e.Plan.Name()
	if e.Function {
		name = e.Plan.Operation().Routine()
	}
	spec := callRendering("Polygon "+name+" namespace "+e.Namespace, e.Children(), name, e.Children())
	if !e.Function {
		spec.Form = expr.FormInfix
	}
	spec.Namespace, spec.Replay.QuoteNamespace, spec.Replay.Validate = e.Namespace, true, e.Validate
	// Bare point literals beside a polygon can resolve to the distinct
	// polygon/polygon overload. Retain the selected formal types in replay,
	// including CHECK rebinding, rather than inferring from literal text.
	for i := range spec.Args {
		spec.Args[i].Transport = expr.TransportCast
		spec.Args[i].TransportType = e.Args[i].ResultUsage()
	}
	return spec, nil
}
func (e Polygon) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(Polygon)
	return ok && e.Plan == o.Plan && e.Namespace == o.Namespace && e.Function == o.Function && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e Polygon) LooksLikeFunction() bool { return e.Function }
func (e Polygon) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Polygon", children, 2); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (e Polygon) ClosureTransparent() {}
func (e Polygon) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	want := expr.CaptureOperator
	if e.Function {
		want = expr.CaptureFunction
	}
	if kind != want {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}

var _ expr.Composite = Polygon{}
var _ expr.Renderable = Polygon{}
