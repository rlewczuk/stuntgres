package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (n ReplicationOrigin) IntrinsicVolatility() expr.Volatility {
	if n.Kind == ReplicationOriginOID {
		return expr.Stable
	}
	return expr.Volatile
}
func (ReplicationOrigin) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (ReplicationOrigin) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (ReplicationOrigin) LooksLikeFunction() bool                 { return true }
func (ReplicationOrigin) CallCost() int                           { return 1 }
func (ReplicationOrigin) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (ReplicationOrigin) SourceAdmission() (bool, bool)           { return true, false }
func (n ReplicationOrigin) Children() []expr.Expr                 { return n.Args[:min(n.Arity, 2)] }
func (n ReplicationOrigin) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(ReplicationOrigin)
	if !ok || n.Kind != o.Kind || n.Arity != o.Arity {
		return false
	}
	for i := range int(n.Arity) {
		if !same(n.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}
func (n ReplicationOrigin) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != int(n.Arity) || len(children) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid replication origin reconstruction")
	}
	n.Info = info
	for i, child := range children {
		n.Args[i] = child
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}
func (n ReplicationOrigin) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n ReplicationOrigin) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, n.Arity)
	for i := range args {
		args[i] = expr.RenderArg{Expr: n.Args[i]}
	}
	return expr.Rendering{Tree: "ReplicationOrigin", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}
func (n ReplicationOrigin) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: int(n.Arity), Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n ReplicationOrigin) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= int(n.Arity) || written >= 2 {
		return nil, fmt.Errorf("stuntgres: invalid replication origin capture")
	}
	return n.Args[written], nil
}
func (n ReplicationOrigin) ValidateStoredProvider() error { return n.Validate() }
