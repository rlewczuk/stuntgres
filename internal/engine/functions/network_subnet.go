package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// NetworkSubnet is an infix scalar, not an invocable SQL function or SRF.
type NetworkSubnet struct {
	expr.Info
	Plan      sqltype.NetworkSubnetPlan
	Args      [2]expr.Expr
	Namespace string
}

func (e NetworkSubnet) Eval(c expr.Context) (sqltype.Value, error) {
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
func NewNetworkSubnet(info expr.Info, op sqltype.NetworkSubnetOperator, args [2]expr.Expr, namespace string) (NetworkSubnet, error) {
	if missingArgument(args[:]) {
		return NetworkSubnet{}, fmt.Errorf("stuntgres: missing network subnet operand")
	}
	plan, err := sqltype.BindNetworkSubnet(op, info.Usage, args[0].ResultUsage(), args[1].ResultUsage())
	return NetworkSubnet{info, plan, args, namespace}, err
}
func (e NetworkSubnet) Reconstruct(info expr.Info, args [2]expr.Expr) (NetworkSubnet, error) {
	return NewNetworkSubnet(info, e.Plan.Operation(), args, e.Namespace)
}

// NetworkSubnet implements expr.Composite.
var _ expr.Composite = NetworkSubnet{}

func (e NetworkSubnet) Children() []expr.Expr { return e.Args[:len(e.Args)] }
func (e NetworkSubnet) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("NetworkSubnet", children, len(e.Args)); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}
func (e NetworkSubnet) Validate() error {
	n, err := e.Reconstruct(e.Info, e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored network subnet operator provider")
	}
	return nil
}

func (e NetworkSubnet) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e NetworkSubnet) ValidateStoredProvider() error { return e.Validate() }

var _ expr.Renderable = NetworkSubnet{}

// Render implements expr.Renderable.
func (e NetworkSubnet) Render() (expr.Rendering, error) {
	spec := callRendering("NetworkSubnet "+e.Plan.Name()+" namespace "+e.Namespace, e.Args[:], e.Plan.Name(), e.Args[:])
	spec.Form, spec.Namespace = expr.FormInfix, e.Namespace
	spec.Replay.QuoteNamespace = true
	if missingArgument(e.Args[:]) {
		spec.Replay.Validate = refuseReplay("stuntgres: missing network subnet operator operand")
	} else {
		spec.Replay.Validate = e.Validate
	}
	return spec, nil
}

// SameGroup implements expr.GroupIdentity.
func (e NetworkSubnet) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(NetworkSubnet)
	return ok && e.Plan == o.Plan && e.Namespace == o.Namespace && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e NetworkSubnet) LooksLikeFunction() bool { return false }

// Substitute implements expr.Substitutable.
func (e NetworkSubnet) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("NetworkSubnet", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info = info
	copy(e.Args[:], children)
	return e, nil
}

// ClosureTransparent implements expr.ClosureTransparent.
func (e NetworkSubnet) ClosureTransparent() {}

func (e NetworkSubnet) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: [2]expr.Expr{e.Args[0], e.Args[1]}, Count: 2}, true, nil
}
