package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/catalog"
)

func validateInstalledUUIDOssp(n plan.InstalledUUIDOsspExpr, v catalog.Read) error {
	if !analyze.ValidInstalledUUIDOsspShape(n) || v.Get(n.Ref.Address) != n.Procedure {
		return fmt.Errorf("stuntgres: invalid stored uuid-ossp identity")
	}
	return extension.StockUUIDOssp.ValidateProcedure(v, n.Procedure)
}
