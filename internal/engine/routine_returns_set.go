package engine

import (
	"stuntgres/internal/engine/catalog"
)

func procedureReturnsSet(d *catalog.Procedure) bool {
	return d.Builtin != nil && d.Builtin.ReturnsSet ||
		intaggProviderOf(d) == stockIntagg && d.RoutineKind == catalog.FunctionRoutine &&
			d.Name() == "int_array_enum" && d.Signature == "1007" && d.Result == 23
}
