package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledText(n plan.InstalledTextExpr, v catalog.Read) error {
	_, symbol, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if symbol != n.Function || !analyze.ValidInstalledText(n) {
		return fmt.Errorf("stuntgres: invalid installed text capture")
	}
	return nil
}
