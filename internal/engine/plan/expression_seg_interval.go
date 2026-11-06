package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledSegIntervalExpr retains the exact installed routine through every
// reconstruction; the selected-operator wrapper separately owns operator syntax.
type InstalledSegIntervalExpr struct {
	functions.SegInterval
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (e InstalledSegIntervalExpr) Eval(c expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalInstalledSegInterval(e)
}
func (e InstalledSegIntervalExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	n, err := e.SegInterval.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.SegInterval = n.(functions.SegInterval)
	return e, nil
}
func (e InstalledSegIntervalExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e InstalledSegIntervalExpr) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledSegIntervalExpr)
	return ok && e.Procedure == o.Procedure && e.Ref == o.Ref && e.SegInterval.SameGroup(o.SegInterval, same, definition)
}
