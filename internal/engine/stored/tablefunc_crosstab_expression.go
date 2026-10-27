package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledTablefuncCrosstab(n plan.InstalledTablefuncCrosstabExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure || len(n.Args) < 1 || len(n.Args) > 2 {
		return fmt.Errorf("stuntgres: invalid crosstab expression")
	}
	for i, arg := range n.Args {
		id := sqltype.Text
		if i == 1 {
			id = sqltype.Int4
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid crosstab argument")
		}
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if err := extension.ValidateTablefuncIdentity(v, current, n.Ref); err != nil {
		return err
	}
	signature := catalog.ProcedureSignature("25")
	if len(n.Args) == 2 {
		signature = "25,23"
	}
	if current.Signature != signature {
		return fmt.Errorf("stuntgres: invalid crosstab arity")
	}
	shape := n.Usage.Type
	if shape == nil || current.Result != 2249 && !shape.IsComposite() {
		return fmt.Errorf("stuntgres: invalid crosstab result shape")
	}
	if current.Result != 2249 {
		typ, ok := v.LookupType(sqltype.ID(current.Result))
		if !ok || shape != typ {
			return fmt.Errorf("stuntgres: invalid crosstab named result generation")
		}
	} else if shape.ID() != 2249 {
		return fmt.Errorf("stuntgres: invalid crosstab record result")
	}
	return nil
}
