package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledPgvisibilityExpr struct {
	functions.Pgvisibility
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (n InstalledPgvisibilityExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalPgvisibility(InstalledPgvisibilityExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed pg_visibility runtime")
	}
	return runtime.EvalPgvisibility(n)
}
func (n InstalledPgvisibilityExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	runtime, ok := ctx.(interface {
		OpenPgvisibility(InstalledPgvisibilityExpr) (expr.SourceCursor, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing installed pg_visibility source runtime")
	}
	return runtime.OpenPgvisibility(n)
}
func (n InstalledPgvisibilityExpr) ScalarSourceOutName() string {
	if !n.Kind.Check() {
		return ""
	}
	outputs := n.Procedure.OutputDeclaration()
	return outputs.Name(outputs.Inputs())
}
func (n InstalledPgvisibilityExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pg_visibility lost installed identity")
	}
	call, err := n.Pgvisibility.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.Pgvisibility = call.(functions.Pgvisibility)
	return n, nil
}
func (n InstalledPgvisibilityExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgvisibilityExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgvisibilityExpr)
	if !ok || n.Ref != o.Ref || n.Procedure != o.Procedure || n.Kind != o.Kind || n.Slots != o.Slots {
		return false
	}
	if n.Kind.Check() || n.Kind == functions.VisibilityTruncateMap {
		return n.ResultUsage() == o.ResultUsage()
	}
	return sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
