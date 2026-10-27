package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (GenericFileRead) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (GenericFileRead) PlanningFold() expr.Fold { return expr.FoldNever }
func (n GenericFileRead) StrictNull() expr.Strict { if n.Kind == GenericFileStat { return expr.StrictNone }; return expr.StrictAfterBare }
func (n GenericFileRead) PreflightFold() expr.Preflight { if n.Kind == GenericFileStat { return expr.PreflightChildren }; return expr.PreflightNullBare }
func (GenericFileRead) LooksLikeFunction() bool { return true }
func (GenericFileRead) CallCost() int { return 1 }
func (GenericFileRead) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (GenericFileRead) SourceAdmission() (bool, bool) { return true, false }
func (n GenericFileRead) Children() []expr.Expr { return n.Args[:n.Arity] }
func (n GenericFileRead) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
func (n GenericFileRead) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(GenericFileRead)
	return ok && n.Kind == o.Kind && n.Arity == o.Arity && n.Slots == o.Slots && n.ResultUsage() == o.ResultUsage()
}
func (n GenericFileRead) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(GenericFileRead)
	if !ok || n.Kind != o.Kind || n.Arity != o.Arity || n.Slots != o.Slots { return false }
	for i := range int(n.Arity) { if !same(n.Args[i], o.Args[i]) { return false } }
	return true
}
func (n GenericFileRead) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != int(n.Arity) { return nil, fmt.Errorf("stuntgres: invalid generic file read reconstruction") }
	n.Info = info
	for i, child := range children { if child == nil { return nil, fmt.Errorf("stuntgres: nil generic file read operand") }; n.Args[i] = child }
	return n, nil
}
func (n GenericFileRead) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	rebuilt := next.(GenericFileRead)
	if err := rebuilt.Validate(); err != nil { return nil, err }
	return rebuilt, nil
}
func (n GenericFileRead) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	args := make([]expr.RenderArg, n.Arity)
	for i := range args {
		args[i] = expr.RenderArg{Expr: n.Args[i]}
		if n.Kind == GenericFileStat && n.Slots[0] != 0 {
			args[i].Label = [2]string{"filename", "missing_ok"}[n.Slots[i]]
		}
	}
	return expr.Rendering{Tree: "GenericFileRead", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}
func (n GenericFileRead) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	return expr.CaptureOperands{Count: int(n.Arity), Indexed: true}, true, nil
}
func (n GenericFileRead) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= int(n.Arity) { return nil, fmt.Errorf("stuntgres: invalid generic file read capture") }
	return n.Args[written], nil
}
func (n GenericFileRead) ValidateStoredProvider() error { return n.Validate() }
