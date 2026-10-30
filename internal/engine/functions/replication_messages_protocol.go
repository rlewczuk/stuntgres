package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (ReplicationMessages) IntrinsicVolatility() expr.Volatility    { return expr.Volatile }
func (ReplicationMessages) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (ReplicationMessages) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (ReplicationMessages) LooksLikeFunction() bool                 { return true }
func (ReplicationMessages) CallCost() int                           { return 1 }
func (ReplicationMessages) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (ReplicationMessages) SourceAdmission() (bool, bool)           { return true, false }
func (n ReplicationMessages) Children() []expr.Expr                 { return n.Args[:min(n.Arity, 4)] }
func (n ReplicationMessages) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(ReplicationMessages)
	if !ok || n.Kind != o.Kind || n.Arity != o.Arity || n.Slots != o.Slots {
		return false
	}
	for i := range int(n.Arity) {
		if !same(n.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}
func (n ReplicationMessages) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != int(n.Arity) || len(children) > 4 {
		return nil, fmt.Errorf("stuntgres: invalid replication messages reconstruction")
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
func (n ReplicationMessages) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n ReplicationMessages) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, n.Arity)
	named := false
	for i := range int(n.Arity) {
		named = named || n.Slots[i] != uint8(i)
	}
	for i := range args {
		args[i] = expr.RenderArg{Expr: n.Args[i]}
		if named {
			args[i].Label = replicationMessagesLabels[n.Slots[i]]
		}
	}
	return expr.Rendering{Tree: "ReplicationMessages", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}
func (n ReplicationMessages) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil {
		return expr.CaptureOperands{}, false, err
	}
	return expr.CaptureOperands{Count: int(n.Arity), Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n ReplicationMessages) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= int(n.Arity) || written >= 4 {
		return nil, fmt.Errorf("stuntgres: invalid replication messages capture")
	}
	return n.Args[written], nil
}
func (n ReplicationMessages) ValidateStoredProvider() error { return n.Validate() }
