package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledPgsurgeryExpr retains symbolic declaration identity, never authority.
type InstalledPgsurgeryExpr struct {
	functions.Pgsurgery
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgsurgeryExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalPgsurgery(InstalledPgsurgeryExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed pg_surgery runtime")
	}
	return runtime.EvalPgsurgery(n)
}
func (n InstalledPgsurgeryExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pg_surgery lost installed identity")
	}
	call, err := n.Pgsurgery.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.Pgsurgery = call.(functions.Pgsurgery)
	return n, nil
}
func (n InstalledPgsurgeryExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgsurgeryExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgsurgeryExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.Slots == o.Slots && n.Operation == o.Operation && n.ResultUsage() == o.ResultUsage()
}
