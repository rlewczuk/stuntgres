package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledPgrowlocksExpr retains the selected installation through structural
// rewrites. Admission validates it against the current catalog and ACL.
type InstalledPgrowlocksExpr struct {
	functions.Pgrowlocks
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgrowlocksExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pgrowlocks lost installed identity")
	}
	next, err := n.Pgrowlocks.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.Pgrowlocks = next.(functions.Pgrowlocks)
	return n, nil
}
func (n InstalledPgrowlocksExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}

func (n InstalledPgrowlocksExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenPgrowlocks(InstalledPgrowlocksExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed pgrowlocks runtime")
	}
	return runtime.OpenPgrowlocks(n)
}

func (n InstalledPgrowlocksExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgrowlocksExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}

func (n InstalledPgrowlocksExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledPgrowlocksExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && same(n.Arg, o.Arg)
}
