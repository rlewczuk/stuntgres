package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledPgstattupleStatsExpr struct {
	functions.PgstattupleStats
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgstattupleStatsExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalPgstattupleStats(InstalledPgstattupleStatsExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed pgstatindex runtime")
	}
	return runtime.EvalPgstattupleStats(n)
}
func (n InstalledPgstattupleStatsExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	v, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(v), nil
}
func (n InstalledPgstattupleStatsExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pgstatindex lost installed identity")
	}
	call, err := n.PgstattupleStats.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.PgstattupleStats = call.(functions.PgstattupleStats)
	return n, nil
}
func (n InstalledPgstattupleStatsExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgstattupleStatsExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgstattupleStatsExpr)
	return ok && n.Operation == o.Operation && n.Input == o.Input && n.Ref == o.Ref && n.Procedure == o.Procedure && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n InstalledPgstattupleStatsExpr) ScalarSourceOutName() string { return n.Procedure.Name() }
