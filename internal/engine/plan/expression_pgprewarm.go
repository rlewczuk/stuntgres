package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledPgPrewarmExpr struct {
	functions.PgPrewarm
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}
func (n InstalledPgPrewarmExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface { EvalPgPrewarm(InstalledPgPrewarmExpr) (sqltype.Value, error) })
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed pg_prewarm runtime") }
	return runtime.EvalPgPrewarm(n)
}
func (n InstalledPgPrewarmExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
func (n InstalledPgPrewarmExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure { return nil, fmt.Errorf("stuntgres: pg_prewarm lost installed identity") }
	call, err := n.PgPrewarm.Rebuild(info, children)
	if err != nil { return nil, err }
	n.PgPrewarm = call.(functions.PgPrewarm)
	return n, nil
}
func (n InstalledPgPrewarmExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (n InstalledPgPrewarmExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgPrewarmExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.Slots == o.Slots && n.ResultUsage() == o.ResultUsage()
}
func (InstalledPgPrewarmExpr) JumbleFields(s expr.StatementFields) { s.JumbleText("plan.InstalledPgPrewarmExpr") }
