package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InstalledPgStatStatementsResetExpr retains genuine declaration authority; the
// current statement host alone borrows the cluster accumulator at body demand.
type InstalledPgStatStatementsResetExpr struct {
	functions.PgStatStatementsReset
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgStatStatementsResetExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if n.Procedure == nil || info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: statement statistics reset lost installed identity")
	}
	next, err := n.PgStatStatementsReset.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.PgStatStatementsReset = next.(functions.PgStatStatementsReset)
	return n, nil
}
func (n InstalledPgStatStatementsResetExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgStatStatementsResetExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalPgStatStatementsReset(InstalledPgStatStatementsResetExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed statement statistics reset runtime")
	}
	return runtime.EvalPgStatStatementsReset(n)
}
func (n InstalledPgStatStatementsResetExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
func (n InstalledPgStatStatementsResetExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgStatStatementsResetExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.Order == o.Order && n.WrittenCount == o.WrittenCount && n.Labels == o.Labels && n.ResultUsage() == o.ResultUsage()
}
func (n InstalledPgStatStatementsResetExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(InstalledPgStatStatementsResetExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Order != o.Order || n.WrittenCount != o.WrittenCount || n.Labels != o.Labels {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
