package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (RecoveryControl) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (RecoveryControl) PlanningFold() expr.Fold { return expr.FoldNever }
func (RecoveryControl) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (RecoveryControl) LooksLikeFunction() bool { return true }
func (RecoveryControl) CallCost() int { return 1 }
func (RecoveryControl) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (RecoveryControl) SourceAdmission() (bool, bool) { return true, false }
func (n RecoveryControl) Children() []expr.Expr { return n.Args[:min(n.Arity, 2)] }
func (n RecoveryControl) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(RecoveryControl)
	if !ok || n.Kind != o.Kind || n.Arity != o.Arity || n.Slots != o.Slots { return false }
	for i := range int(n.Arity) { if !same(n.Args[i], o.Args[i]) { return false } }
	return true
}
func (n RecoveryControl) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != int(n.Arity) { return nil, fmt.Errorf("stuntgres: invalid recovery control reconstruction") }
	n.Info = info
	for i, child := range children {
		if i >= 2 || child == nil { return nil, fmt.Errorf("stuntgres: invalid recovery control child") }
		n.Args[i] = child
	}
	if err := n.Validate(); err != nil { return nil, err }
	return n, nil
}
func (n RecoveryControl) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Substitute(info, children) }
func (n RecoveryControl) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	args := make([]expr.RenderArg, n.Arity)
	named := false
	for i := range int(n.Arity) { named = named || n.Slots[i] != uint8(i) }
	for i := range args {
		args[i] = expr.RenderArg{Expr: n.Args[i]}
		if named { args[i].Label = recoveryControlLabels[n.Slots[i]] }
	}
	return expr.Rendering{Tree: "RecoveryControl", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}
func (n RecoveryControl) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil { return expr.CaptureOperands{}, false, err }
	return expr.CaptureOperands{Count: int(n.Arity), Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n RecoveryControl) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= int(n.Arity) || written >= 2 { return nil, fmt.Errorf("stuntgres: invalid recovery control capture") }
	return n.Args[written], nil
}
func (n RecoveryControl) ValidateStoredProvider() error { return n.Validate() }
