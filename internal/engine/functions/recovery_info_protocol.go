package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (RecoveryInfo) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (RecoveryInfo) PlanningFold() expr.Fold { return expr.FoldNever }
func (RecoveryInfo) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (RecoveryInfo) LooksLikeFunction() bool { return true }
func (RecoveryInfo) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (RecoveryInfo) SourceAdmission() (bool, bool) { return true, false }
func (RecoveryInfo) Children() []expr.Expr { return nil }
func (RecoveryInfo) GroupLeaf() {}
func (n RecoveryInfo) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(RecoveryInfo)
	return ok && n.Kind == o.Kind
}
func (n RecoveryInfo) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 { return nil, fmt.Errorf("stuntgres: invalid recovery inquiry children") }
	n.Info = info
	return n, nil
}
func (n RecoveryInfo) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	rebuilt := next.(RecoveryInfo)
	return rebuilt, rebuilt.Validate()
}
func (n RecoveryInfo) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	return expr.Rendering{Tree: "RecoveryInfo", Form: expr.FormCall, Name: n.Kind.Name()}, nil
}
func (RecoveryInfo) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n RecoveryInfo) ValidateStoredProvider() error { return n.Validate() }

func (WALResourceManagers) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (WALResourceManagers) PlanningFold() expr.Fold { return expr.FoldNever }
func (WALResourceManagers) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (WALResourceManagers) LooksLikeFunction() bool { return true }
func (WALResourceManagers) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (WALResourceManagers) SourceAdmission() (bool, bool) { return true, true }
func (WALResourceManagers) Children() []expr.Expr { return nil }
func (WALResourceManagers) GroupLeaf() {}
func (n WALResourceManagers) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(WALResourceManagers)
	_ = o
	return ok
}
func (n WALResourceManagers) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(WALResourceManagers)
	return ok && n.ResultUsage() == o.ResultUsage()
}
func (n WALResourceManagers) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 { return nil, fmt.Errorf("stuntgres: invalid recovery inquiry children") }
	n.Info = info
	return n, nil
}
func (n WALResourceManagers) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil { return nil, err }
	rebuilt := next.(WALResourceManagers)
	return rebuilt, rebuilt.Validate()
}
func (n WALResourceManagers) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil { return expr.Rendering{}, err }
	return expr.Rendering{Tree: "WALResourceManagers", Form: expr.FormCall, Name: "pg_get_wal_resource_managers"}, nil
}
func (WALResourceManagers) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n WALResourceManagers) ValidateStoredProvider() error { return n.Validate() }

