package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
)

func procedureReturnsSet(d *catalog.Procedure) bool {
	return d.Builtin != nil && d.Builtin.ReturnsSet ||
		extension.CitextProviderOf(d) == extension.StockCitext && d.Installed.ReturnsSet ||
		extension.PgrowlocksProviderOf(d) == extension.StockPgrowlocks ||
		extension.AmcheckProviderOf(d) == extension.StockAmcheck ||
		extension.IntaggProviderOf(d) == extension.StockIntagg && d.RoutineKind == catalog.FunctionRoutine &&
			d.Name() == "int_array_enum" && d.Signature == "1007" && d.Result == 23
}
