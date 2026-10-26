package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledTablefuncNormal(n plan.InstalledTablefuncNormalExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Float8 {
		return fmt.Errorf("stuntgres: invalid normal_rand expression")
	}
	for i, arg := range n.Args {
		want := sqltype.Float8
		if i == 0 { want = sqltype.Int4 }
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.BaseType().ID() != want { return fmt.Errorf("stuntgres: invalid normal_rand operand") }
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil { return err }
	return extension.ValidateTablefuncIdentity(v, current, n.Ref)
}
