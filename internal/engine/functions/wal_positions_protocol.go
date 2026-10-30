package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
)

func (WALPosition) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (WALPosition) PlanningFold() expr.Fold { return expr.FoldNever }
func (WALPosition) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (WALPosition) LooksLikeFunction() bool { return true }
func (WALPosition) CallCost() int { return 1 }
func (WALPosition) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (WALPosition) SourceAdmission() (bool, bool) { return true, false }
func (n WALPosition) Children() []expr.Expr {
	if n.Arg == nil { return nil }
	return []expr.Expr{n.Arg}
}
func (n WALPosition) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(WALPosition)
	return ok && n.Kind == o.Kind && (n.Arg == nil && o.Arg == nil || n.Arg != nil && o.Arg != nil && same(n.Arg, o.Arg))
}
func (n WALPosition) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	count := 0
	if n.Kind == WALRestorePoint { count = 1 }
	if len(children) != count { return nil, fmt.Errorf("stuntgres: invalid WAL position reconstruction") }
	n.Info = info
	n.Arg = nil
	if count == 1 { n.Arg = children[0] }
	if err := n.Validate(); err != nil { return nil, err }
	return n, nil
}
func (n WALPosition) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Substitute(info, children)
}
func (n WALPosition) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	var args []expr.RenderArg
	if n.Arg != nil { args = []expr.RenderArg{{Expr: n.Arg}} }
	return expr.Rendering{Tree: "WALPosition", Inputs: n.Children(), Form: expr.FormCall, Name: n.Kind.Name(), Args: args}, nil
}
func (n WALPosition) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if err := n.Validate(); err != nil { return expr.CaptureOperands{}, false, err }
	count := 0
	if n.Arg != nil { count = 1 }
	return expr.CaptureOperands{Count: count, Indexed: true}, kind == expr.CaptureFunction, nil
}
func (n WALPosition) CaptureOperand(written int) (expr.Expr, error) {
	if err := n.Validate(); err != nil { return nil, err }
	if written != 0 || n.Arg == nil { return nil, fmt.Errorf("stuntgres: invalid WAL position capture") }
	return n.Arg, nil
}
func (n WALPosition) ValidateStoredProvider() error { return n.Validate() }
