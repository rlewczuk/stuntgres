package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/catalog"
)

func ValidateInstalledUUIDOssp(n plan.InstalledUUIDOsspExpr, v catalog.Read) error {
	proc, symbol, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	member, known := functions.UUIDOsspLookup(symbol)
	if err != nil || !known || member.Selector != n.Name || !analyze.ValidInstalledUUIDOsspShape(n) {
		return fmt.Errorf("stuntgres: invalid stored uuid-ossp identity")
	}
	return extension.StockUUIDOssp.ValidateProcedure(v, proc)
}
