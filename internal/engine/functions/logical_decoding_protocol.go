package functions

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/expr"
)

func (LogicalDecoding) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (LogicalDecoding) PlanningFold() expr.Fold { return expr.FoldNever }
func (LogicalDecoding) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (LogicalDecoding) LooksLikeFunction() bool { return true }
func (LogicalDecoding) CallCost() int { return 1000 }
func (LogicalDecoding) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (LogicalDecoding) SourceAdmission() (bool, bool) { return true, true }
func (n LogicalDecoding) Children() []expr.Expr { return slices.Clone(n.Args) }
func (n LogicalDecoding) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(LogicalDecoding)
	return ok && n.Kind == o.Kind && n.Variadic == o.Variadic && slices.Equal(n.Slots, o.Slots) && slices.Equal(n.Labels, o.Labels) && n.ResultUsage() == o.ResultUsage()
}
func (n LogicalDecoding) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(LogicalDecoding)
	if !ok || !n.SameSourceOccurrence(o) || len(n.Args) != len(o.Args) { return false }
	for i := range n.Args { if !same(n.Args[i], o.Args[i]) { return false } }
	return true
}
func (n LogicalDecoding) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(n.Args) { return nil, fmt.Errorf("stuntgres: invalid logical decoding reconstruction") }
	for _, child := range children { if child == nil { return nil, fmt.Errorf("stuntgres: nil logical decoding operand") } }
	n.Info, n.Args = info, slices.Clone(children)
	return n, nil
}
func (n LogicalDecoding) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	rebuilt := next.(LogicalDecoding)
	if err := rebuilt.Validate(); err != nil { return nil, err }
	return rebuilt, nil
}
func (n LogicalDecoding) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg}
		if len(n.Labels) != 0 { args[i].Label = n.Labels[i] }
	}
	return expr.Rendering{Tree: "LogicalDecoding", Inputs: n.Children(), Form: expr.FormCall, Name: logicalDecodingNames[n.Kind], Args: args, Variadic: n.Variadic}, nil
}
func (n LogicalDecoding) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	return expr.CaptureOperands{Count: len(n.Args), Indexed: true}, true, nil
}
func (n LogicalDecoding) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= len(n.Args) { return nil, fmt.Errorf("stuntgres: invalid logical decoding capture") }
	return n.Args[written], nil
}
func (n LogicalDecoding) ValidateStoredProvider() error { return n.Validate() }
