package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledCubeSpatial(n plan.InstalledCubeSpatialExpr, view catalog.Read) error {
	if n.Procedure == nil || n.Ref.Address != n.Procedure.Address() || n.Ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid stored cube spatial reference")
	}
	if err := extension.ValidateCubeSpatialCall(view, n.Procedure, n.CubeSpatial); err != nil {
		return err
	}
	return extension.StockCube.ValidateProcedure(view, n.Procedure)
}
