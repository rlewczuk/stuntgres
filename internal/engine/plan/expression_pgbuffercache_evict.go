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

type InstalledPgBuffercacheEvictionExpr struct {
	functions.PgBuffercacheEviction
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}
func (n InstalledPgBuffercacheEvictionExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface { EvalPgBuffercacheEviction(InstalledPgBuffercacheEvictionExpr) (sqltype.Value, error) })
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: missing pg_buffercache eviction runtime") }
	return runtime.EvalPgBuffercacheEviction(n)
}
func (n InstalledPgBuffercacheEvictionExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface { OpenPgBuffercacheEviction(InstalledPgBuffercacheEvictionExpr) (expr.SourceCursor, error) })
	if !ok { return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing pg_buffercache eviction source runtime") }
	return runtime.OpenPgBuffercacheEviction(n)
}
func (n InstalledPgBuffercacheEvictionExpr) AdvanceSource(ctx context.Context, evaluation expr.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || cursor.State == nil { return cursor.Next(ctx) }
	runtime, ok := evaluation.(interface { AdvancePgBuffercacheEviction(InstalledPgBuffercacheEvictionExpr, context.Context, *expr.SourceCursor) (sqltype.Value, bool, error) })
	if !ok { cursor.Close(); return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing pg_buffercache eviction source authority") }
	return runtime.AdvancePgBuffercacheEviction(n, ctx, cursor)
}
func (n InstalledPgBuffercacheEvictionExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure { return nil, fmt.Errorf("stuntgres: pg_buffercache eviction lost installed identity") }
	call, err := n.PgBuffercacheEviction.Rebuild(info, children)
	if err != nil { return nil, err }
	n.PgBuffercacheEviction = call.(functions.PgBuffercacheEviction)
	return n, nil
}
func (n InstalledPgBuffercacheEvictionExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (n InstalledPgBuffercacheEvictionExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgBuffercacheEvictionExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Kind != o.Kind || n.Source != o.Source || n.Occurrence != o.Occurrence || len(n.Args) != len(o.Args) || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) {
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
func (n InstalledPgBuffercacheEvictionExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(InstalledPgBuffercacheEvictionExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Kind != o.Kind || len(n.Args) != len(o.Args) || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (n InstalledPgBuffercacheEvictionExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgBuffercacheEvictionExpr")
}
