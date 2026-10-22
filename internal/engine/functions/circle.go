package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Circle owns only actual circle scalar signatures, including strict radius.
// Function is syntax, not authority: Info retains the selected routine.
type Circle struct {
	expr.Info
	Plan      sqltype.CirclePlan
	Args      [2]expr.Expr
	Namespace string
	Function  bool
}

func NewCircle(info expr.Info, op sqltype.CircleOperator, args [2]expr.Expr, namespace string, function bool) (Circle, error) {
	if missingArgument(args[:op.Arity()]) || op.Arity() == 1 && args[1] != nil {
		return Circle{}, fmt.Errorf("stuntgres: invalid circle operands")
	}
	var right sqltype.Usage
	if op.Arity() == 2 {
		right = args[1].ResultUsage()
	}
	p, err := sqltype.BindCircle(op, info.Usage, args[0].ResultUsage(), right)
	return Circle{info, p, args, namespace, function}, err
}
func (e Circle) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e Circle) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e Circle) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Circle", children, len(e.Children())); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	copy(args[:], children)
	return rebuilt(NewCircle(info, e.Plan.Operation(), args, e.Namespace, e.Function))
}
func (e Circle) Validate() error {
	n, err := NewCircle(e.Info, e.Plan.Operation(), e.Args, e.Namespace, e.Function)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored circle provider")
	}
	return nil
}
func (e Circle) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e Circle) ValidateStoredProvider() error        { return e.Validate() }
func (e Circle) Render() (expr.Rendering, error) {
	name := e.Plan.Name()
	if e.Function {
		name = e.Plan.Operation().Routine()
	}
	spec := callRendering("Circle "+name+" namespace "+e.Namespace, e.Children(), name, e.Children())
	if !e.Function {
		spec.Form = expr.FormInfix
	}
	spec.Namespace = e.Namespace
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	return spec, nil
}
func (e Circle) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(Circle)
	if !ok || e.Plan != o.Plan || e.Namespace != o.Namespace || e.Function != o.Function {
		return false
	}
	for i, arg := range e.Children() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e Circle) LooksLikeFunction() bool { return e.Function }
func (e Circle) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Circle", children, len(e.Children())); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}
func (e Circle) ClosureTransparent() {}
func (e Circle) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	want := expr.CaptureOperator
	if e.Function {
		want = expr.CaptureFunction
	}
	if kind != want {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: len(e.Children())}, true, nil
}

var _ expr.Composite = Circle{}
var _ expr.Renderable = Circle{}
