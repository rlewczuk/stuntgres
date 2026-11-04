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
	captured, err := ExpressionRoutineDescriptor(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if captured != n.Procedure {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed cube spatial carrier")
	}
	current, err := ValidateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return sqltype.Value{}, err
	}
	// Preserve original source coordinates; only this validation projection
	// names the current descriptor. The executable carrier stays unchanged.
	boundary := n.CubeSpatial
	boundary.RoutineReference = current
	if err := extension.ValidateCubeSpatialCall(view, current, boundary); err != nil {
		return sqltype.Value{}, err
	}
	return n.CubeSpatial.Eval(c)
}
