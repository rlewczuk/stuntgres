package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledPgstatindexExpr struct {
	functions.Pgstatindex
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgstatindexExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalPgstatindex(InstalledPgstatindexExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed pgstatindex runtime")
	}
	return runtime.EvalPgstatindex(n)
}
func (n InstalledPgstatindexExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	v, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(v), nil
}
func (n InstalledPgstatindexExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pgstatindex lost installed identity")
	}
	call, err := n.Pgstatindex.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.Pgstatindex = call.(functions.Pgstatindex)
	return n, nil
}
func (n InstalledPgstatindexExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgstatindexExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgstatindexExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n InstalledPgstatindexExpr) ScalarSourceOutName() string { return n.Procedure.Name() }
