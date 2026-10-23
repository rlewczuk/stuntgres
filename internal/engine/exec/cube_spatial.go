package exec

import (
	"fmt"

	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalInstalledCubeSpatial(n plan.InstalledCubeSpatialExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.Ref.Address != n.Procedure.Address() || n.Ref.Address.SubID != 0 || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed cube spatial reference")
	}
	if err := extension.ValidateCubeSpatialCall(view, n.Procedure, n.CubeSpatial); err != nil {
		return sqltype.Value{}, err
	}
	return n.CubeSpatial.Eval(c)
}
