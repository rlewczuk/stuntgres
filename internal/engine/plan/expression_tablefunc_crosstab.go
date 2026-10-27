package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledTablefuncCrosstabExpr struct {
	functions.TablefuncCrosstab
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledTablefuncCrosstabExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: crosstab lost installed identity")
	}
	next, err := n.TablefuncCrosstab.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.TablefuncCrosstab = next.(functions.TablefuncCrosstab)
	return n, nil
}
func (n InstalledTablefuncCrosstabExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledTablefuncCrosstabExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenTablefuncCrosstab(InstalledTablefuncCrosstabExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed crosstab runtime")
	}
	return runtime.OpenTablefuncCrosstab(n)
}
func (n InstalledTablefuncCrosstabExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledTablefuncCrosstabExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n InstalledTablefuncCrosstabExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledTablefuncCrosstabExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) || len(n.Args) != len(o.Args) {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
