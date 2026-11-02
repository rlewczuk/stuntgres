package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledDblinkGetPkey(n plan.InstalledDblinkGetPkeyExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure {
		return fmt.Errorf("stuntgres: invalid dblink_get_pkey expression")
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if err := extension.ValidateDblinkIdentity(v, current, n.Ref); err != nil {
		return err
	}
	call := n.DblinkGetPkey
	call.RoutineReference = current
	return extension.ValidateDblinkGetPkeyCall(v, current, call)
}
