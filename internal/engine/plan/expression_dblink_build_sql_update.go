package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledDblinkBuildSQLUpdateExpr struct {
	functions.DblinkBuildSQLUpdate
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledDblinkBuildSQLUpdateExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalDblinkBuildSQLUpdate(InstalledDblinkBuildSQLUpdateExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed dblink runtime")
	}
	return runtime.EvalDblinkBuildSQLUpdate(n)
}
func (n InstalledDblinkBuildSQLUpdateExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (n InstalledDblinkBuildSQLUpdateExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: dblink constructor lost installed identity")
	}
	call, err := n.DblinkBuildSQLUpdate.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.DblinkBuildSQLUpdate = call.(functions.DblinkBuildSQLUpdate)
	return n, nil
}
func (n InstalledDblinkBuildSQLUpdateExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledDblinkBuildSQLUpdateExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledDblinkBuildSQLUpdateExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.ResultUsage() == o.ResultUsage()
}
func (InstalledDblinkBuildSQLUpdateExpr) ScalarSourceOutName() string { return "" }
