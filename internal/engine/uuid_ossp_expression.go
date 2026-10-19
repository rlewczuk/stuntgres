package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The embedded selector shares UUID kernels; SQL authority and stored recipes
// always use the captured installed procedure, never a builtin alias or literal.
type installedUUIDOsspFunctionExpression struct {
	functions.UUIDGeneration
	procedure *catalog.Procedure
	ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e installedUUIDOsspFunctionExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.UUIDGeneration.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.UUIDGeneration = node.(functions.UUIDGeneration)
	return e, nil
}

func (n installedUUIDOsspFunctionExpression) validShape() bool {
	properties := n.Properties()
	// Group rewriting adds execution-stage dependence to a valid leaf.
	// DEFAULT closure separately rejects that dependence before shape checks.
	properties.StageDependent = false
	if n.procedure == nil {
		return false
	}
	selector, volatility, selected := functions.UUIDOsspLookup(n.procedure.Name())
	return selected && n.RoutineReference == n.procedure && uuidOsspProviderOf(n.procedure) == stockUUIDOssp && n.ref.Address == n.procedure.Address() && n.ref.Epoch != 0 && n.Name == selector && len(n.Args) == 0 && n.Timestamp == nil && n.Addition == (sqltype.Arithmetic{}) && n.ResultUsage().Type != nil && n.ResultUsage().Type.ID() == sqltype.UUID && n.ResultUsage().Valid() && properties == (expr.Properties{Volatility: volatility})
}

func (n installedUUIDOsspFunctionExpression) validate(v catalog.Read) error {
	if !n.validShape() || v.Get(n.ref.Address) != n.procedure {
		return fmt.Errorf("stuntgres: invalid stored uuid-ossp identity")
	}
	return stockUUIDOssp.validateProcedure(v, n.procedure)
}
