package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledConnectby(n plan.InstalledConnectbyExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure || n.Usage.Type == nil || n.Usage.Type.ID() != 2249 {
		return fmt.Errorf("stuntgres: invalid connectby expression")
	}
	if _, err := n.Connectby.Rebuild(n.Info, n.Args); err != nil {
		return err
	}
	if n.Defined && !n.Usage.Type.IsComposite() {
		return fmt.Errorf("stuntgres: invalid connectby record definition")
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if err := extension.ValidateTablefuncIdentity(v, current, n.Ref); err != nil {
		return err
	}
	signature := catalog.ProcedureSignature("25,25,25,25,23")
	if n.Ordered {
		signature = "25,25,25,25,25,23"
	}
	if n.Branch {
		signature += ",25"
	}
	if current.Signature != signature || current.Result != 2249 {
		return fmt.Errorf("stuntgres: mismatched connectby variant")
	}
	return nil
}
