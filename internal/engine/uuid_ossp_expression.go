package engine

import (
	"fmt"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func validInstalledUUIDOsspShape(n plan.InstalledUUIDOsspExpr) bool {
	properties := n.Properties()
	// Group rewriting adds execution-stage dependence to a valid leaf.
	// DEFAULT closure separately rejects that dependence before shape checks.
	properties.StageDependent = false
	if n.Procedure == nil {
		return false
	}
	selector, volatility, selected := functions.UUIDOsspLookup(n.Procedure.Name())
	return selected && n.RoutineReference == n.Procedure && extension.UUIDOsspProviderOf(n.Procedure) == extension.StockUUIDOssp && n.Ref.Address == n.Procedure.Address() && n.Ref.Epoch != 0 && n.Name == selector && len(n.Args) == 0 && n.Timestamp == nil && n.Addition == (sqltype.Arithmetic{}) && n.ResultUsage().Type != nil && n.ResultUsage().Type.ID() == sqltype.UUID && n.ResultUsage().Valid() && properties == (expr.Properties{Volatility: volatility})
}

func validateInstalledUUIDOssp(n plan.InstalledUUIDOsspExpr, v catalog.Read) error {
	if !validInstalledUUIDOsspShape(n) || v.Get(n.Ref.Address) != n.Procedure {
		return fmt.Errorf("stuntgres: invalid stored uuid-ossp identity")
	}
	return extension.StockUUIDOssp.ValidateProcedure(v, n.Procedure)
}
