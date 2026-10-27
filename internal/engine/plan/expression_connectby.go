package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledConnectbyExpr struct {
	functions.Connectby
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledConnectbyExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: connectby lost installed identity")
	}
	next, err := n.Connectby.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.Connectby = next.(functions.Connectby)
	return n, nil
}
func (n InstalledConnectbyExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledConnectbyExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenConnectby(InstalledConnectbyExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed connectby runtime")
	}
	return runtime.OpenConnectby(n)
}
func (n InstalledConnectbyExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledConnectbyExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Ordered != o.Ordered || n.Branch != o.Branch || n.Defined != o.Defined || n.Occurrence != o.Occurrence || n.Source != o.Source || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) || len(n.Args) != len(o.Args) {
		return false
	}
	for i, arg := range n.Args {
		a, b := expr.InfoOf(arg), expr.InfoOf(o.Args[i])
		if a.Source != b.Source || a.Pos != b.Pos || a.Occurrence != b.Occurrence || a.Usage != b.Usage {
			return false
		}
	}
	return true
}
func (n InstalledConnectbyExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledConnectbyExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Ordered != o.Ordered || n.Branch != o.Branch || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) || len(n.Args) != len(o.Args) {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
