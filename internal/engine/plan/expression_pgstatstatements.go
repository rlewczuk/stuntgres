package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledPgStatStatementsExpr struct {
	functions.PgStatStatements
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgStatStatementsExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: statement statistics lost installed identity")
	}
	next, err := n.PgStatStatements.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.PgStatStatements = next.(functions.PgStatStatements)
	return n, nil
}
func (n InstalledPgStatStatementsExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgStatStatementsExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenPgStatStatements(InstalledPgStatStatementsExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed statement statistics runtime")
	}
	return runtime.OpenPgStatStatements(n)
}
func (n InstalledPgStatStatementsExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalPgStatStatements(InstalledPgStatStatementsExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed statement statistics runtime")
	}
	return runtime.EvalPgStatStatements(n)
}
func (n InstalledPgStatStatementsExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgStatStatementsExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.InfoOnly == o.InfoOnly && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n InstalledPgStatStatementsExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledPgStatStatementsExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.InfoOnly == o.InfoOnly && (n.InfoOnly || same(n.ShowText, o.ShowText))
}
