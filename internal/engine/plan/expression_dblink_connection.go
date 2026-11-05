package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledDblinkConnectionExpr struct {
	functions.DblinkConnection
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledDblinkConnectionExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalDblinkConnection(InstalledDblinkConnectionExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed dblink runtime")
	}
	return runtime.EvalDblinkConnection(n)
}
func (n InstalledDblinkConnectionExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (n InstalledDblinkConnectionExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: dblink connection lost installed identity")
	}
	call, err := n.DblinkConnection.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.DblinkConnection = call.(functions.DblinkConnection)
	return n, nil
}
func (n InstalledDblinkConnectionExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledDblinkConnectionExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledDblinkConnectionExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.Operation == o.Operation && n.Count == o.Count && n.ResultUsage() == o.ResultUsage()
}
func (InstalledDblinkConnectionExpr) ScalarSourceOutName() string { return "" }
func (n InstalledDblinkConnectionExpr) JumbleFields(s expr.StatementFields) {
	s.JumbleText("plan.InstalledDblinkConnectionExpr")
	n.DblinkConnection.JumbleFields(s)
}
