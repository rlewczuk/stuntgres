package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (n RuntimeCapability) IntrinsicVolatility() expr.Volatility { if n.NUMA { return expr.Stable }; return expr.Volatile }
func (LoadedModules) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (CurrentLogfile) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (RuntimeCapability) PlanningFold() expr.Fold { return expr.FoldNever }
func (LoadedModules) PlanningFold() expr.Fold { return expr.FoldNever }
func (CurrentLogfile) PlanningFold() expr.Fold { return expr.FoldNever }
func (RuntimeCapability) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (LoadedModules) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (CurrentLogfile) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (RuntimeCapability) LooksLikeFunction() bool { return true }
func (LoadedModules) LooksLikeFunction() bool { return true }
func (CurrentLogfile) LooksLikeFunction() bool { return true }
func (RuntimeCapability) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (LoadedModules) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (CurrentLogfile) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (RuntimeCapability) SourceAdmission() (bool, bool) { return true, false }
func (LoadedModules) SourceAdmission() (bool, bool) { return true, true }
func (CurrentLogfile) SourceAdmission() (bool, bool) { return true, false }
func (RuntimeCapability) Children() []expr.Expr { return nil }
func (LoadedModules) Children() []expr.Expr { return nil }
func (n CurrentLogfile) Children() []expr.Expr { if n.Format == nil { return nil }; return []expr.Expr{n.Format} }
func (RuntimeCapability) GroupLeaf() {}
func (LoadedModules) GroupLeaf() {}
func (n RuntimeCapability) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool { o, ok := other.(RuntimeCapability); return ok && n.NUMA == o.NUMA }
func (LoadedModules) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool { _, ok := other.(LoadedModules); return ok }
func (n CurrentLogfile) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(CurrentLogfile)
	if !ok || (n.Format == nil) != (o.Format == nil) { return false }
	return n.Format == nil || same(n.Format, o.Format)
}
func (n LoadedModules) SameSourceOccurrence(other expr.Expr) bool { o, ok := other.(LoadedModules); return ok && n.ResultUsage() == o.ResultUsage() }
func (n RuntimeCapability) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 { return nil, fmt.Errorf("stuntgres: invalid runtime capability children") }; n.Info = info; return n, nil
}
func (n LoadedModules) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 { return nil, fmt.Errorf("stuntgres: invalid loaded modules children") }; n.Info = info; return n, nil
}
func (n CurrentLogfile) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	count := 0; if n.Format != nil { count = 1 }
	if len(children) != count || count == 1 && children[0] == nil { return nil, fmt.Errorf("stuntgres: invalid current logfile children") }
	n.Info = info; if count == 1 { n.Format = children[0] }; return n, nil
}
func (n RuntimeCapability) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) { next, err := n.Substitute(info, children); if err != nil { return nil, err }; v := next.(RuntimeCapability); return v, v.Validate() }
func (n LoadedModules) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) { next, err := n.Substitute(info, children); if err != nil { return nil, err }; v := next.(LoadedModules); return v, v.Validate() }
func (n CurrentLogfile) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) { next, err := n.Substitute(info, children); if err != nil { return nil, err }; v := next.(CurrentLogfile); return v, v.Validate() }
func (n RuntimeCapability) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }; name := "pg_jit_available"; if n.NUMA { name = "pg_numa_available" }; return expr.Rendering{Tree: "RuntimeCapability", Form: expr.FormCall, Name: name}, nil
}
func (n LoadedModules) Render() (expr.Rendering, error) { if err := n.Validate(); err != nil { return expr.Rendering{}, err }; return expr.Rendering{Tree: "LoadedModules", Form: expr.FormCall, Name: "pg_get_loaded_modules"}, nil }
func (n CurrentLogfile) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	r := expr.Rendering{Tree: "CurrentLogfile", Form: expr.FormCall, Name: "pg_current_logfile", Inputs: n.Children()}
	if n.Format != nil { r.Args = []expr.RenderArg{{Expr: n.Format}} }; return r, nil
}
func (RuntimeCapability) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) { return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil }
func (LoadedModules) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) { return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil }
func (n CurrentLogfile) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	if n.Format == nil { return expr.CaptureOperands{}, true, nil }; return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Format}}, true, nil
}
func (n RuntimeCapability) ValidateStoredProvider() error { return n.Validate() }
func (n LoadedModules) ValidateStoredProvider() error { return n.Validate() }
func (n CurrentLogfile) ValidateStoredProvider() error { return n.Validate() }
