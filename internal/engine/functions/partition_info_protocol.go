package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (PartitionTree) IntrinsicVolatility() expr.Volatility      { return expr.Volatile }
func (PartitionAncestors) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PartitionRoot) IntrinsicVolatility() expr.Volatility      { return expr.Immutable }

// Even IMMUTABLE pg_partition_root consults a captured catalog. Do not compute
// non-NULL results into a reusable plan; strict NULL still eliminates admission.
func (PartitionRoot) PlanningFold() expr.Fold                          { return expr.FoldNever }
func (PartitionTree) PlanningFold() expr.Fold                          { return expr.FoldNever }
func (PartitionAncestors) PlanningFold() expr.Fold                     { return expr.FoldNever }
func (PartitionRoot) StrictNull() expr.Strict                          { return expr.StrictAfter }
func (PartitionRoot) PreflightFold() expr.Preflight                    { return expr.PreflightSimplify }
func (PartitionTree) PreflightFold() expr.Preflight                    { return expr.PreflightChildren }
func (PartitionAncestors) PreflightFold() expr.Preflight               { return expr.PreflightChildren }
func (PartitionTree) LooksLikeFunction() bool                          { return true }
func (PartitionAncestors) LooksLikeFunction() bool                     { return true }
func (PartitionRoot) LooksLikeFunction() bool                          { return true }
func (PartitionRoot) CallCost() int                                    { return 1 }
func (n PartitionRoot) ClosedDefault(closed func(expr.Expr) bool) bool { return closed(n.Arg) }
func (PartitionTree) ClosedDefault(func(expr.Expr) bool) bool          { return false }
func (PartitionAncestors) ClosedDefault(func(expr.Expr) bool) bool     { return false }
func (PartitionTree) SourceAdmission() (bool, bool)                    { return true, true }
func (PartitionAncestors) SourceAdmission() (bool, bool)               { return true, true }
func (PartitionRoot) SourceAdmission() (bool, bool)                    { return true, false }
func (n PartitionTree) Children() []expr.Expr                          { return []expr.Expr{n.Arg} }
func (n PartitionAncestors) Children() []expr.Expr                     { return []expr.Expr{n.Arg} }
func (n PartitionRoot) Children() []expr.Expr                          { return []expr.Expr{n.Arg} }
func (n PartitionTree) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(PartitionTree)
	return ok && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n PartitionAncestors) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(PartitionAncestors)
	return ok && n.ResultUsage() == o.ResultUsage()
}
func (n PartitionTree) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(PartitionTree)
	return ok && same(n.Arg, o.Arg)
}
func (n PartitionAncestors) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(PartitionAncestors)
	return ok && same(n.Arg, o.Arg)
}
func (n PartitionRoot) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(PartitionRoot)
	return ok && same(n.Arg, o.Arg)
}
func partitionInfoChild(children []expr.Expr) error {
	if len(children) != 1 || children[0] == nil {
		return fmt.Errorf("stuntgres: invalid partition inquiry arguments")
	}
	return nil
}
func (n PartitionTree) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := partitionInfoChild(children); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n PartitionAncestors) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := partitionInfoChild(children); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n PartitionRoot) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := partitionInfoChild(children); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n PartitionTree) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(PartitionTree)
	return rebuilt, rebuilt.Validate()
}
func (n PartitionAncestors) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(PartitionAncestors)
	return rebuilt, rebuilt.Validate()
}
func (n PartitionRoot) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	rebuilt := next.(PartitionRoot)
	return rebuilt, rebuilt.Validate()
}
func partitionInfoRendering(tree, name string, arg expr.Expr) expr.Rendering {
	return expr.Rendering{Tree: tree, Inputs: []expr.Expr{arg}, Form: expr.FormCall, Name: name, Args: []expr.RenderArg{{Expr: arg}}, Replay: expr.Replay{CatalogSnapshot: true}}
}
func (n PartitionTree) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return partitionInfoRendering("PartitionTree", "pg_partition_tree", n.Arg), nil
}
func (n PartitionAncestors) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return partitionInfoRendering("PartitionAncestors", "pg_partition_ancestors", n.Arg), nil
}
func (n PartitionRoot) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return partitionInfoRendering("PartitionRoot", "pg_partition_root", n.Arg), nil
}
func partitionInfoCapture(kind expr.CaptureKind, arg expr.Expr) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{arg}}, true, nil
}
func (n PartitionTree) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return partitionInfoCapture(kind, n.Arg)
}
func (n PartitionAncestors) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return partitionInfoCapture(kind, n.Arg)
}
func (n PartitionRoot) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return partitionInfoCapture(kind, n.Arg)
}
func (n PartitionTree) ValidateStoredProvider() error      { return n.Validate() }
func (n PartitionAncestors) ValidateStoredProvider() error { return n.Validate() }
func (n PartitionRoot) ValidateStoredProvider() error      { return n.Validate() }
func (PartitionTree) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
func (PartitionAncestors) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
func (PartitionRoot) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, true, false
}
