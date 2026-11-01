package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledPgpArmorHeaders(n plan.InstalledPgpArmorHeadersExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure || n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid pgp_armor_headers expression")
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if err := extension.ValidatePgpArmorHeadersIdentity(v, current, n.Ref); err != nil {
		return err
	}
	if err := functions.ValidatePgpArmorShape(n.Usage); err != nil {
		return err
	}
	text, ok := v.LookupType(sqltype.Text)
	if !ok || n.Arg.ResultUsage().Type != text {
		return fmt.Errorf("stuntgres: foreign pgp_armor_headers input generation")
	}
	for i := range 2 {
		if n.Usage.Type.CompositeMember(i).Usage != text.Usage() {
			return fmt.Errorf("stuntgres: foreign pgp_armor_headers OUT generation")
		}
	}
	return nil
}
