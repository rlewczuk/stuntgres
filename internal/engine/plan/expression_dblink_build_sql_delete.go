package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledDblinkBuildSQLDeleteExpr struct {
	functions.DblinkBuildSQLDelete
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}

func (n InstalledDblinkBuildSQLDeleteExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalDblinkBuildSQLDelete(InstalledDblinkBuildSQLDeleteExpr) (sqltype.Value, error)
	})
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed dblink runtime") }
	return runtime.EvalDblinkBuildSQLDelete(n)
}
func (n InstalledDblinkBuildSQLDeleteExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}
func (n InstalledDblinkBuildSQLDeleteExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure { return nil, fmt.Errorf("stuntgres: dblink constructor lost installed identity") }
	call, err := n.DblinkBuildSQLDelete.Rebuild(info, children)
	if err != nil { return nil, err }
	n.DblinkBuildSQLDelete = call.(functions.DblinkBuildSQLDelete)
	return n, nil
}
func (n InstalledDblinkBuildSQLDeleteExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledDblinkBuildSQLDeleteExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledDblinkBuildSQLDeleteExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.ResultUsage() == o.ResultUsage()
}
func (InstalledDblinkBuildSQLDeleteExpr) ScalarSourceOutName() string { return "" }
