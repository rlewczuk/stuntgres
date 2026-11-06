package exec

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalInstalledSegInterval(n plan.InstalledSegIntervalExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.Ref.Address != n.Procedure.Address() || n.Ref.Address.SubID != 0 || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed seg interval reference")
	}
	captured, err := ExpressionRoutineDescriptor(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if captured != n.Procedure {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed seg interval carrier")
	}
	current, err := ValidateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return sqltype.Value{}, err
	}
	// This boundary-only value has no captured reference or recipe stamp. The
	// original expression remains the executable carrier after validation.
	boundary := functions.SegInterval{
		Info: expr.Info{Usage: n.ResultUsage(), RoutineReference: current},
		Plan: n.Plan,
		Args: n.Args,
	}
	if err := extension.ValidateSegIntervalCall(view, current, boundary); err != nil {
		return sqltype.Value{}, err
	}
	return n.SegInterval.Eval(c)
}
