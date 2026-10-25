package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledVerifyHeapamExpr struct {
	functions.VerifyHeapam
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}
func (n InstalledVerifyHeapamExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure { return nil, fmt.Errorf("stuntgres: verify_heapam lost installed identity") }
	next, err := n.VerifyHeapam.Rebuild(info, children)
	if err != nil { return nil, err }
	n.VerifyHeapam = next.(functions.VerifyHeapam)
	return n, nil
}
func (n InstalledVerifyHeapamExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (n InstalledVerifyHeapamExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface { OpenVerifyHeapam(InstalledVerifyHeapamExpr) (expr.SourceCursor, error) })
	if !ok { return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed verify_heapam runtime") }
	return runtime.OpenVerifyHeapam(n)
}
func (n InstalledVerifyHeapamExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledVerifyHeapamExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n InstalledVerifyHeapamExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledVerifyHeapamExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure { return false }
	for i, arg := range n.Args { if !same(arg, o.Args[i]) { return false } }
	return true
}
