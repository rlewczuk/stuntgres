package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (SizeInquiry) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (SizeInquiry) PlanningFold() expr.Fold { return expr.FoldNever }
func (SizeInquiry) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (SizeInquiry) LooksLikeFunction() bool { return true }
func (SizeInquiry) CallCost() int { return 1 }
func (SizeInquiry) SourceAdmission() (bool, bool) { return true, false }
func (n SizeInquiry) ClosedDefault(closed func(expr.Expr) bool) bool {
	return closed(n.Arg) && (n.Fork == nil || closed(n.Fork))
}
func (n SizeInquiry) Children() []expr.Expr {
	if n.Fork == nil { return []expr.Expr{n.Arg} }
	return []expr.Expr{n.Arg, n.Fork}
}
func (n SizeInquiry) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(SizeInquiry)
	return ok && n.Kind == o.Kind && same(n.Arg, o.Arg) && (n.Fork == nil && o.Fork == nil || n.Fork != nil && o.Fork != nil && same(n.Fork, o.Fork))
}
func (n SizeInquiry) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	count := 1
	if n.Fork != nil { count = 2 }
	if len(children) != count || children[0] == nil || count == 2 && children[1] == nil {
		return nil, fmt.Errorf("stuntgres: invalid size inquiry children")
	}
	n.Info, n.Arg = info, children[0]
	if count == 2 { n.Fork = children[1] }
	return n, nil
}
func (n SizeInquiry) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	rebuilt := next.(SizeInquiry)
	if err := rebuilt.Validate(); err != nil { return nil, err }
	return rebuilt, nil
}
func (n SizeInquiry) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	args := []expr.RenderArg{{Expr: n.Arg}}
	if n.Fork != nil { args = append(args, expr.RenderArg{Expr: n.Fork}) }
	return expr.Rendering{Tree: "SizeInquiry", Inputs: n.Children(), Form: expr.FormCall, Name: sizeNames[n.Kind], Args: args, Replay: expr.Replay{CatalogSnapshot: true}}, nil
}
func (n SizeInquiry) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	count := 1
	if n.Fork != nil { count = 2 }
	return expr.CaptureOperands{Count: count, Inline: [2]expr.Expr{n.Arg, n.Fork}}, true, nil
}
func (n SizeInquiry) ValidateStoredProvider() error { return n.Validate() }
func (SizeInquiry) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) { return true, true, false }
