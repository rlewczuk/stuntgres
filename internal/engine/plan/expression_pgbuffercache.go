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

type InstalledPgBuffercacheExpr struct {
	functions.PgBuffercache
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgBuffercacheExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pg_buffercache lost installed identity")
	}
	call, err := n.PgBuffercache.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.PgBuffercache = call.(functions.PgBuffercache)
	return n, nil
}
func (n InstalledPgBuffercacheExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgBuffercacheExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenPgBuffercache(InstalledPgBuffercacheExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed pg_buffercache runtime")
	}
	return runtime.OpenPgBuffercache(n)
}
func (n InstalledPgBuffercacheExpr) AdvanceSource(ctx context.Context, evaluation expr.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted {
		return cursor.Next(ctx)
	}
	runtime, ok := evaluation.(interface {
		AdvancePgBuffercache(InstalledPgBuffercacheExpr, context.Context, *expr.SourceCursor) (sqltype.Value, bool, error)
	})
	if !ok {
		cursor.Close()
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing pg_buffercache source authority")
	}
	return runtime.AdvancePgBuffercache(n, ctx, cursor)
}
func (n InstalledPgBuffercacheExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgBuffercacheExpr)
	return ok && n.Kind == o.Kind && n.Ref == o.Ref && n.Procedure == o.Procedure && sqltype.SameRecordUsage(n.Usage, o.Usage)
}
func (n InstalledPgBuffercacheExpr) SameGroup(other expr.Expr, _ func(expr.Expr, expr.Expr) bool, _ bool) bool {
	return n.SameSourceOccurrence(other)
}
func (n InstalledPgBuffercacheExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgBuffercacheExpr")
	s.JumbleUint64(uint64(n.Kind))
}
