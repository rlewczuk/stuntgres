package functions

import (
	"stuntgres/internal/engine/expr"
)

func (CatalogUserName) IntrinsicVolatility() expr.Volatility             { return expr.Stable }
func (CatalogForeignKeys) IntrinsicVolatility() expr.Volatility          { return expr.Stable }
func (CatalogUserName) PlanningFold() expr.Fold                          { return expr.FoldNever }
func (CatalogForeignKeys) PlanningFold() expr.Fold                       { return expr.FoldNever }
func (CatalogUserName) PreflightFold() expr.Preflight                    { return expr.PreflightChildren }
func (CatalogForeignKeys) PreflightFold() expr.Preflight                 { return expr.PreflightKeep }
func (CatalogUserName) LooksLikeFunction() bool                          { return true }
func (CatalogForeignKeys) LooksLikeFunction() bool                       { return true }
func (CatalogUserName) CallCost() int                                    { return 10 }
func (n CatalogUserName) ClosedDefault(closed func(expr.Expr) bool) bool { return closed(n.Arg) }
func (CatalogForeignKeys) ClosedDefault(func(expr.Expr) bool) bool       { return false }
func (n CatalogUserName) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(CatalogUserName)
	return ok && same(n.Arg, o.Arg)
}
func (CatalogForeignKeys) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	_, ok := other.(CatalogForeignKeys)
	return ok
}
func (n CatalogUserName) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "CatalogUserName", Inputs: n.Children(), Form: expr.FormCall, Name: "pg_get_userbyid", Args: []expr.RenderArg{{Expr: n.Arg}}, Replay: expr.Replay{CatalogSnapshot: true}}, nil
}
func (n CatalogForeignKeys) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "CatalogForeignKeys", Form: expr.FormCall, Name: "pg_get_catalog_foreign_keys", Replay: expr.Replay{CatalogSnapshot: true}}, nil
}
func (n CatalogUserName) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
func (CatalogForeignKeys) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n CatalogForeignKeys) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if !changed {
		return n, false, nil
	}
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, false, err
	}
	n = next.(CatalogForeignKeys)
	n.metadata, err = newCatalogForeignKeyValues(info.Usage.Type)
	if err != nil {
		return nil, false, err
	}
	return n, true, nil
}
func (n CatalogUserName) ValidateStoredProvider() error    { return n.Validate() }
func (n CatalogForeignKeys) ValidateStoredProvider() error { return n.Validate() }
func (CatalogUserName) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
func (CatalogForeignKeys) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
