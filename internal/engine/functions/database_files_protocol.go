package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (DatabaseFilesInquiry) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (DatabaseFilesInquiry) PlanningFold() expr.Fold { return expr.FoldNever }
func (DatabaseFilesInquiry) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (DatabaseFilesInquiry) LooksLikeFunction() bool { return true }
func (DatabaseFilesInquiry) CallCost() int { return 1 }
func (DatabaseFilesInquiry) SourceAdmission() (bool, bool) { return true, false }
func (n DatabaseFilesInquiry) ClosedDefault(closed func(expr.Expr) bool) bool {
	return closed(n.Arg) && (n.Second == nil || closed(n.Second))
}
func (n DatabaseFilesInquiry) Children() []expr.Expr {
	if n.Second == nil { return []expr.Expr{n.Arg} }
	return []expr.Expr{n.Arg, n.Second}
}
func (n DatabaseFilesInquiry) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(DatabaseFilesInquiry)
	return ok && n.Kind == o.Kind && same(n.Arg, o.Arg) && (n.Second == nil && o.Second == nil || n.Second != nil && o.Second != nil && same(n.Second, o.Second))
}
func (n DatabaseFilesInquiry) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	count := 1
	if n.Second != nil { count = 2 }
	if len(children) != count || children[0] == nil || count == 2 && children[1] == nil {
		return nil, fmt.Errorf("stuntgres: invalid database files inquiry children")
	}
	n.Info, n.Arg = info, children[0]
	if count == 2 { n.Second = children[1] }
	return n, nil
}
func (n DatabaseFilesInquiry) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	rebuilt := next.(DatabaseFilesInquiry)
	if err := rebuilt.Validate(); err != nil { return nil, err }
	return rebuilt, nil
}
func (n DatabaseFilesInquiry) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	args := []expr.RenderArg{{Expr: n.Arg}}
	if n.Second != nil { args = append(args, expr.RenderArg{Expr: n.Second}) }
	return expr.Rendering{Tree: "DatabaseFilesInquiry", Inputs: n.Children(), Form: expr.FormCall, Name: databaseFilesNames[n.Kind], Args: args, Replay: expr.Replay{CatalogSnapshot: true}}, nil
}
func (n DatabaseFilesInquiry) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	count := 1
	if n.Second != nil { count = 2 }
	return expr.CaptureOperands{Count: count, Inline: [2]expr.Expr{n.Arg, n.Second}}, true, nil
}
func (n DatabaseFilesInquiry) ValidateStoredProvider() error { return n.Validate() }
func (DatabaseFilesInquiry) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) { return true, true, false }
