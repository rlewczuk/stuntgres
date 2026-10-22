package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
)

func (TablespaceLocation) IntrinsicVolatility() expr.Volatility             { return expr.Stable }
func (TablespaceDatabases) IntrinsicVolatility() expr.Volatility            { return expr.Stable }
func (TablespaceLocation) PlanningFold() expr.Fold                          { return expr.FoldNever }
func (TablespaceDatabases) PlanningFold() expr.Fold                         { return expr.FoldNever }
func (TablespaceLocation) PreflightFold() expr.Preflight                    { return expr.PreflightChildren }
func (TablespaceDatabases) PreflightFold() expr.Preflight                   { return expr.PreflightChildren }
func (TablespaceLocation) LooksLikeFunction() bool                          { return true }
func (TablespaceDatabases) LooksLikeFunction() bool                         { return true }
func (TablespaceLocation) CallCost() int                                    { return 10 }
func (n TablespaceLocation) ClosedDefault(closed func(expr.Expr) bool) bool { return closed(n.Arg) }
func (TablespaceDatabases) ClosedDefault(func(expr.Expr) bool) bool         { return false }
func (TablespaceLocation) SourceAdmission() (bool, bool)                    { return true, false }
func (TablespaceDatabases) SourceAdmission() (bool, bool)                   { return true, true }
func (n TablespaceLocation) Children() []expr.Expr                          { return []expr.Expr{n.Arg} }
func (n TablespaceDatabases) Children() []expr.Expr                         { return []expr.Expr{n.Arg} }

// SourcePosition is compared by the lowering owner; this identifies the typed
// provider/result of that same written occurrence after child reconstruction.
func (n TablespaceDatabases) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(TablespaceDatabases)
	return ok && n.ResultUsage() == o.ResultUsage()
}
func (n TablespaceLocation) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(TablespaceLocation)
	return ok && same(n.Arg, o.Arg)
}
func (n TablespaceDatabases) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(TablespaceDatabases)
	return ok && same(n.Arg, o.Arg)
}
func (n TablespaceLocation) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil {
		return nil, fmt.Errorf("stuntgres: invalid tablespace location arguments")
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n TablespaceDatabases) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil {
		return nil, fmt.Errorf("stuntgres: invalid tablespace databases arguments")
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n TablespaceLocation) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(TablespaceLocation)
	if err := rebuilt.Validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}
func (n TablespaceDatabases) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(TablespaceDatabases)
	if err := rebuilt.Validate(); err != nil {
		return nil, err
	}
	return rebuilt, nil
}
func (n TablespaceLocation) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "TablespaceLocation", Inputs: n.Children(), Form: expr.FormCall, Name: "pg_tablespace_location", Args: []expr.RenderArg{{Expr: n.Arg}}, Replay: expr.Replay{CatalogSnapshot: true}}, nil
}
func (n TablespaceDatabases) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "TablespaceDatabases", Inputs: n.Children(), Form: expr.FormCall, Name: "pg_tablespace_databases", Args: []expr.RenderArg{{Expr: n.Arg}}, Replay: expr.Replay{CatalogSnapshot: true}}, nil
}
func (n TablespaceLocation) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
func (n TablespaceDatabases) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{n.Arg}}, true, nil
}
func (n TablespaceLocation) ValidateStoredProvider() error  { return n.Validate() }
func (n TablespaceDatabases) ValidateStoredProvider() error { return n.Validate() }
func (TablespaceLocation) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
func (TablespaceDatabases) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
