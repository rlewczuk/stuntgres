package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledEarthPointExpr retains exact installed callable authority; a
// SelectedOperatorExpr additionally retains the actual operator identity.
type InstalledEarthPointExpr struct {
	functions.EarthPoint
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (e InstalledEarthPointExpr) Eval(c expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalInstalledEarthPoint(e)
}
func (e InstalledEarthPointExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	n, err := e.EarthPoint.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.EarthPoint = n.(functions.EarthPoint)
	return e, nil
}
func (e InstalledEarthPointExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e InstalledEarthPointExpr) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledEarthPointExpr)
	return ok && e.Procedure == o.Procedure && e.Ref == o.Ref && e.EarthPoint.SameGroup(o.EarthPoint, same, definition)
}
