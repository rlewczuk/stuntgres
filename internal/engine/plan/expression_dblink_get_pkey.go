package plan

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledDblinkGetPkeyExpr retains immutable declaration identity, not approval.
type InstalledDblinkGetPkeyExpr struct {
	functions.DblinkGetPkey
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (InstalledDblinkGetPkeyExpr) SourceResultDeclaration() bool { return true }

func (n InstalledDblinkGetPkeyExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: dblink_get_pkey lost installed identity")
	}
	next, err := n.DblinkGetPkey.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.DblinkGetPkey = next.(functions.DblinkGetPkey)
	return n, nil
}
func (n InstalledDblinkGetPkeyExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledDblinkGetPkeyExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenDblinkGetPkey(InstalledDblinkGetPkeyExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed dblink_get_pkey runtime")
	}
	return runtime.OpenDblinkGetPkey(n)
}
func (n InstalledDblinkGetPkeyExpr) AdvanceSource(ctx context.Context, evaluation expr.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted {
		return cursor.Next(ctx)
	}
	runtime, ok := evaluation.(interface {
		AdvanceDblinkGetPkey(InstalledDblinkGetPkeyExpr, context.Context, *expr.SourceCursor) (sqltype.Value, bool, error)
	})
	if !ok {
		cursor.Close()
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing dblink source authority")
	}
	return runtime.AdvanceDblinkGetPkey(n, ctx, cursor)
}
func (n InstalledDblinkGetPkeyExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledDblinkGetPkeyExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.ResultUsage() == o.ResultUsage()
}
func (n InstalledDblinkGetPkeyExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledDblinkGetPkeyExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.ResultUsage() == o.ResultUsage() && same(n.Arg, o.Arg)
}
