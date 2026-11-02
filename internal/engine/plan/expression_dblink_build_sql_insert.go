package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledDblinkBuildSQLInsertExpr struct {
	functions.DblinkBuildSQLInsert
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledDblinkBuildSQLInsertExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalDblinkBuildSQLInsert(InstalledDblinkBuildSQLInsertExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed dblink runtime")
	}
	return runtime.EvalDblinkBuildSQLInsert(n)
}
func (n InstalledDblinkBuildSQLInsertExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (n InstalledDblinkBuildSQLInsertExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: dblink constructor lost installed identity")
	}
	call, err := n.DblinkBuildSQLInsert.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.DblinkBuildSQLInsert = call.(functions.DblinkBuildSQLInsert)
	return n, nil
}
func (n InstalledDblinkBuildSQLInsertExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledDblinkBuildSQLInsertExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledDblinkBuildSQLInsertExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.ResultUsage() == o.ResultUsage()
}
func (InstalledDblinkBuildSQLInsertExpr) ScalarSourceOutName() string { return "" }
