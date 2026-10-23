package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledPgrowlocks(n plan.InstalledPgrowlocksExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure || n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.BaseType().ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid pgrowlocks expression")
	}
	// The carrier remains the original hard capture. Only the current
	// declaration validated against it enters the exact-current provider gate.
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if err := extension.ValidatePgrowlocksIdentity(v, current, n.Ref); err != nil {
		return err
	}
	shape := n.Usage.Type
	names := [6]string{"locked_row", "locker", "multi", "xids", "modes", "pids"}
	types := [6]sqltype.ID{sqltype.Tid, sqltype.Xid, sqltype.Bool, 1011, 1009, 1007}
	if shape == nil || !shape.IsComposite() || shape.CompositeLen() != 6 {
		return fmt.Errorf("stuntgres: invalid pgrowlocks OUT shape")
	}
	for i, name := range names {
		member := shape.CompositeMember(i)
		if member.Attribute != int32(i+1) || member.Name != name || member.Usage.Type == nil || member.Usage.Type.ID() != types[i] {
			return fmt.Errorf("stuntgres: invalid pgrowlocks OUT shape")
		}
	}
	return nil
}
