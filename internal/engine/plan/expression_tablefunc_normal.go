package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// InstalledTablefuncNormalExpr keeps executable authority separate from labels.
type InstalledTablefuncNormalExpr struct {
	functions.TablefuncNormal
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}
func (n InstalledTablefuncNormalExpr) ScalarSourceOutName() string { return n.Procedure.Name() }
func (n InstalledTablefuncNormalExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure { return nil, fmt.Errorf("stuntgres: normal_rand lost installed identity") }
	next, err := n.TablefuncNormal.Rebuild(info, children)
	if err != nil { return nil, err }
	n.TablefuncNormal = next.(functions.TablefuncNormal)
	return n, nil
}
func (n InstalledTablefuncNormalExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (n InstalledTablefuncNormalExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface { OpenTablefuncNormal(InstalledTablefuncNormalExpr) (expr.SourceCursor, error) })
	if !ok { return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed normal_rand runtime") }
	return runtime.OpenTablefuncNormal(n)
}
func (n InstalledTablefuncNormalExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledTablefuncNormalExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Occurrence != o.Occurrence || n.Source != o.Source { return false }
	for i := range n.Args {
		a, b := expr.InfoOf(n.Args[i]), expr.InfoOf(o.Args[i])
		if a.Source != b.Source || a.Pos != b.Pos || a.Occurrence != b.Occurrence || a.Usage != b.Usage { return false }
	}
	return true
}
func (n InstalledTablefuncNormalExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledTablefuncNormalExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure { return false }
	for i := range n.Args { if !same(n.Args[i], o.Args[i]) { return false } }
	return true
}
