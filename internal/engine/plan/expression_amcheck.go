package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledAmcheckExpr retains the selected declaration across all expression
// transports. No relation data or elevated authority is retained by analysis.
type InstalledAmcheckExpr struct {
	functions.Amcheck
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledAmcheckExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalAmcheck(InstalledAmcheckExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed amcheck runtime")
	}
	return runtime.EvalAmcheck(n)
}
func (n InstalledAmcheckExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	call, err := n.Amcheck.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.Amcheck = call.(functions.Amcheck)
	return n, nil
}
func (n InstalledAmcheckExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
