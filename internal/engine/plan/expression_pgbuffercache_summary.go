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

type InstalledPgBuffercacheAggregateExpr struct {
	functions.PgBuffercacheAggregate
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}
func (n InstalledPgBuffercacheAggregateExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface { EvalPgBuffercacheAggregate(InstalledPgBuffercacheAggregateExpr) (sqltype.Value, error) })
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: missing pg_buffercache aggregate runtime") }
	return runtime.EvalPgBuffercacheAggregate(n)
}
func (n InstalledPgBuffercacheAggregateExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface { OpenPgBuffercacheAggregate(InstalledPgBuffercacheAggregateExpr) (expr.SourceCursor, error) })
	if !ok { return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing pg_buffercache aggregate source runtime") }
	return runtime.OpenPgBuffercacheAggregate(n)
}
func (n InstalledPgBuffercacheAggregateExpr) AdvanceSource(ctx context.Context, evaluation expr.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || cursor.State == nil { return cursor.Next(ctx) }
	runtime, ok := evaluation.(interface { AdvancePgBuffercacheAggregate(InstalledPgBuffercacheAggregateExpr, context.Context, *expr.SourceCursor) (sqltype.Value, bool, error) })
	if !ok { cursor.Close(); return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing pg_buffercache aggregate source authority") }
	return runtime.AdvancePgBuffercacheAggregate(n, ctx, cursor)
}
func (n InstalledPgBuffercacheAggregateExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure { return nil, fmt.Errorf("stuntgres: pg_buffercache aggregate lost installed identity") }
	call, err := n.PgBuffercacheAggregate.Rebuild(info, children)
	if err != nil { return nil, err }
	n.PgBuffercacheAggregate = call.(functions.PgBuffercacheAggregate)
	return n, nil
}
func (n InstalledPgBuffercacheAggregateExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (n InstalledPgBuffercacheAggregateExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgBuffercacheAggregateExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Kind != o.Kind || n.Source != o.Source || n.Occurrence != o.Occurrence || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) {
		return false
	}
	return true
}
func (n InstalledPgBuffercacheAggregateExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(InstalledPgBuffercacheAggregateExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Kind != o.Kind || !sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage()) {
		return false
	}
	return true
}
func (n InstalledPgBuffercacheAggregateExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledPgBuffercacheAggregateExpr")
	s.JumbleUint64(uint64(n.Kind))
}