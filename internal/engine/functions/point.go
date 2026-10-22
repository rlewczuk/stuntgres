package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Point owns the selected point predicate; Function records syntax, not authority.
type Point struct {
	expr.Info
	Plan      sqltype.PointPlan
	Args      [2]expr.Expr
	Namespace string
	Function  bool
}

func NewPoint(info expr.Info, op sqltype.PointOperator, args [2]expr.Expr, namespace string, function bool) (Point, error) {
	if missingArgument(args[:]) {
		return Point{}, fmt.Errorf("stuntgres: invalid point operands")
	}
	p, err := sqltype.BindPoint(op, info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	return Point{info, p, args, namespace, function}, err
}
func (e Point) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e Point) Children() []expr.Expr { return e.Args[:] }
func (e Point) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Point", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(NewPoint(info, e.Plan.Operation(), [2]expr.Expr{children[0], children[1]}, e.Namespace, e.Function))
}
func (e Point) Validate() error {
	n, err := NewPoint(e.Info, e.Plan.Operation(), e.Args, e.Namespace, e.Function)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored point provider")
	}
	return nil
}
func (e Point) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e Point) ValidateStoredProvider() error        { return e.Validate() }
func (e Point) Render() (expr.Rendering, error) {
	name := e.Plan.Name()
	if e.Function {
		name = e.Plan.Operation().Routine()
	}
	spec := callRendering("Point "+name+" namespace "+e.Namespace, e.Children(), name, e.Children())
	if !e.Function {
		spec.Form = expr.FormInfix
	}
	spec.Namespace = e.Namespace
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e Point) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(Point)
	if !ok || e.Plan != o.Plan || e.Namespace != o.Namespace || e.Function != o.Function {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e Point) LooksLikeFunction() bool { return e.Function }
func (e Point) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Point", children, 2); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (e Point) ClosureTransparent() {}
func (e Point) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	want := expr.CaptureOperator
	if e.Function {
		want = expr.CaptureFunction
	}
	if kind != want {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}

var _ expr.Composite = Point{}
var _ expr.Renderable = Point{}
