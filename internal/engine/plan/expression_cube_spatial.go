package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledCubeSpatialExpr retains the exact installed routine through every
// reconstruction; the selected-operator wrapper separately owns operator syntax.
type InstalledCubeSpatialExpr struct {
	functions.CubeSpatial
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (e InstalledCubeSpatialExpr) Eval(c expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalInstalledCubeSpatial(e)
}
func (e InstalledCubeSpatialExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	n, err := e.CubeSpatial.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.CubeSpatial = n.(functions.CubeSpatial)
	return e, nil
}
func (e InstalledCubeSpatialExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e InstalledCubeSpatialExpr) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledCubeSpatialExpr)
	return ok && e.Procedure == o.Procedure && e.Ref == o.Ref && e.CubeSpatial.SameGroup(o.CubeSpatial, same, definition)
}
