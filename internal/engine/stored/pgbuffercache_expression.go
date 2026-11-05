package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgBuffercache(n plan.InstalledPgBuffercacheExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure || n.Usage.Type == nil || n.Usage.Type.ID() != 2249 {
		return fmt.Errorf("stuntgres: invalid pg_buffercache expression")
	}
	if n.Usage.Type.IsComposite() {
		for i := range n.Usage.Type.CompositeLen() {
			field := n.Usage.Type.CompositeMember(i).Usage.Type
			if field == nil {
				return fmt.Errorf("stuntgres: missing pg_buffercache record member")
			}
			resident, ok := v.LookupType(field.ID())
			if !ok || field != resident {
				return fmt.Errorf("stuntgres: foreign pg_buffercache record member")
			}
		}
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if err := extension.ValidatePgBuffercacheIdentity(v, current, n.Ref); err != nil {
		return err
	}
	call := n.PgBuffercache
	call.RoutineReference = current
	return extension.ValidatePgBuffercacheCall(v, current, call)
}
