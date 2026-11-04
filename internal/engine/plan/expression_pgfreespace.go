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

type InstalledPgFreespaceExpr struct {
	functions.PgFreespace
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgFreespaceExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalPgFreespace(InstalledPgFreespaceExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed pg_freespace runtime")
	}
	return runtime.EvalPgFreespace(n)
}
func (n InstalledPgFreespaceExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenPgFreespace(InstalledPgFreespaceExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed pg_freespace source runtime")
	}
	return runtime.OpenPgFreespace(n)
}
func (n InstalledPgFreespaceExpr) AdvanceSource(ctx context.Context, evaluation expr.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || !n.Kind.Set() {
		return cursor.Next(ctx)
	}
	runtime, ok := evaluation.(interface {
		AdvancePgFreespace(InstalledPgFreespaceExpr, context.Context, *expr.SourceCursor) (sqltype.Value, bool, error)
	})
	if !ok {
		cursor.Close()
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing pg_freespace source authority")
	}
	return runtime.AdvancePgFreespace(n, ctx, cursor)
}
func (n InstalledPgFreespaceExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pg_freespace lost installed identity")
	}
	call, err := n.PgFreespace.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.PgFreespace = call.(functions.PgFreespace)
	return n, nil
}
func (n InstalledPgFreespaceExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgFreespaceExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgFreespaceExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.Kind == o.Kind && n.Slots == o.Slots && (n.ResultUsage() == o.ResultUsage() || n.Kind.Set() && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()))
}
