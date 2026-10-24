package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// CastRoutine is an actual selected pg_proc invocation, not a typename shortcut.
// Its optional typmod and explicitness operands remain ordinary evaluated args.
type CastRoutine struct {
	expr.Info
	Plan            sqltype.CoercionPlan
	Args            [3]expr.Expr
	Count           int
	Name, Namespace string
	Formals         [3]sqltype.ID
	Result          sqltype.ID
	Volatility      expr.Volatility
}

func NewCastRoutine(info expr.Info, name, namespace string, args []expr.Expr, volatility expr.Volatility) (CastRoutine, error) {
	n := CastRoutine{Info: info, Name: name, Namespace: namespace, Count: len(args), Volatility: volatility}
	if len(args) > 3 || missingArgument(args) || len(args) == 0 && !info.Usage.Type.IsMultirange() {
		return n, fmt.Errorf("invalid cast routine operands")
	}
	copy(n.Args[:], args)
	n.Result = info.Usage.Type.ID()
	for i, arg := range args {
		n.Formals[i] = arg.ResultUsage().Type.ID()
	}
	if len(args) == 0 {
		return n, nil
	}
	var err error
	n.Plan, err = sqltype.PlanCoercion(args[0].ResultUsage(), info.Usage, sqltype.Explicit)
	return n, err
}
func (n CastRoutine) Children() []expr.Expr { return n.Args[:n.Count] }
func (n CastRoutine) Eval(c expr.Context) (sqltype.Value, error) {
	if n.Count == 0 {
		return sqltype.MultirangeFromValues(c.FunctionContext(), n.Usage.Type, nil)
	}
	var values [3]sqltype.Value
	for i, arg := range n.Children() {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	for _, v := range values[:n.Count] {
		if v.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
	}
	target, mode := n.Usage, sqltype.Explicit
	if n.Count > 1 {
		width, err := values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		target.Typmod = int32(width)
	}
	if n.Count > 2 {
		explicit, err := values[2].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
		if !explicit {
			mode = sqltype.Assignment
		}
	}
	p := n.Plan
	if target != n.Usage || mode != sqltype.Explicit {
		var err error
		p, err = n.Plan.Rebind(n.Args[0].ResultUsage(), target, mode)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return p.Apply(c.FunctionContext(), values[0], c.FunctionInput(), c.FunctionOutput())
}
func (n CastRoutine) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("CastRoutine", children, n.Count); err != nil {
		return nil, err
	}
	if info.Usage.Type == nil || info.Usage.Type.ID() != n.Result && !(n.Usage.Type.IsCitext() && info.Usage.Type.IsCitext()) {
		return nil, fmt.Errorf("cast routine result identity changed")
	}
	for i, arg := range children {
		if arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != n.Formals[i] {
			return nil, fmt.Errorf("cast routine formal identity changed")
		}
	}
	next, err := NewCastRoutine(info, n.Name, n.Namespace, children, n.Volatility)
	if err != nil {
		return nil, err
	}
	if n.Count == 0 {
		return next, nil
	}
	next.Plan, err = n.Plan.Rebind(children[0].ResultUsage(), info.Usage, sqltype.Explicit)
	return next, err
}
func (n CastRoutine) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("CastRoutine", children, n.Count); err != nil {
		return nil, err
	}
	n.Info = info
	copy(n.Args[:], children)
	return n, nil
}
func (n CastRoutine) ValidateStoredProvider() error {
	rebuilt, err := n.Rebuild(n.Info, n.Children())
	if err != nil {
		return err
	}
	if !rebuilt.(CastRoutine).Plan.Equal(n.Plan) {
		return fmt.Errorf("cast routine operation changed")
	}
	return nil
}
func (n CastRoutine) IntrinsicVolatility() expr.Volatility { return n.Volatility }
func (CastRoutine) LooksLikeFunction() bool                { return true }
func (CastRoutine) ClosureTransparent()                    {}
func (n CastRoutine) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: n.Children(), Count: n.Count}, true, nil
}
func (n CastRoutine) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(CastRoutine)
	if !ok || n.Name != o.Name || n.Namespace != o.Namespace || n.Count != o.Count || !n.Plan.Equal(o.Plan) {
		return false
	}
	for i, arg := range n.Children() {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (n CastRoutine) Render() (expr.Rendering, error) {
	spec := callRendering("CastRoutine "+n.Namespace+"."+n.Name, n.Children(), n.Name, n.Children())
	transportArgs(spec.Args, expr.TransportLiteral)
	spec.Namespace = n.Namespace
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = n.ValidateStoredProvider
	return spec, nil
}
