package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type InstalledPgstattupleExpr struct {
	functions.Pgstattuple
	Procedure *catalog.Procedure
	Ref ids.ObjectRef
}

func (n InstalledPgstattupleExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, ok := ctx.(interface {
		EvalPgstattuple(InstalledPgstattupleExpr) (sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing installed pgstattuple runtime")
	}
	return runtime.EvalPgstattuple(n)
}
func (n InstalledPgstattupleExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	v, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(v), nil
}
func (n InstalledPgstattupleExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if info.RoutineReference != n.Procedure {
		return nil, fmt.Errorf("stuntgres: pgstattuple lost installed identity")
	}
	call, err := n.Pgstattuple.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	n.Pgstattuple = call.(functions.Pgstattuple)
	return n, nil
}
func (n InstalledPgstattupleExpr) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n InstalledPgstattupleExpr) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(InstalledPgstattupleExpr)
	return ok && n.Ref == o.Ref && n.Procedure == o.Procedure && n.Kind == o.Kind && sqltype.SameRecordUsage(n.ResultUsage(), o.ResultUsage())
}
func (n InstalledPgstattupleExpr) ScalarSourceOutName() string {
	// pg_relpages has no OUT parameter: its range alias names the scalar
	// column. The raw call name supplies the unaliased label even after folding.
	if n.Kind == functions.HeapStatisticsPages {
		return ""
	}
	return n.Procedure.Name()
}
