package exec

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalInstalledEarthPoint(n plan.InstalledEarthPointExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.Ref.Address != n.Procedure.Address() || n.Ref.Address.SubID != 0 || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed earth point reference")
	}
	captured, err := ExpressionRoutineDescriptor(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if captured != n.Procedure {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed earth point carrier")
	}
	current, err := ValidateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return sqltype.Value{}, err
	}
	boundary := functions.EarthPoint{Info: expr.Info{Usage: n.ResultUsage(), RoutineReference: current}, Plan: n.Plan, Args: n.Args}
	if err := extension.ValidateEarthPointCall(view, current, boundary); err != nil {
		return sqltype.Value{}, err
	}
	return n.EarthPoint.Eval(c)
}
