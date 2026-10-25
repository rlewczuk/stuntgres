package stored

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledVerifyHeapam(n plan.InstalledVerifyHeapamExpr, v catalog.Read) error {
	if n.Procedure == nil || n.RoutineReference != n.Procedure { return fmt.Errorf("stuntgres: invalid verify_heapam expression") }
	for i, typ := range [...]sqltype.ID{sqltype.Regclass, sqltype.Bool, sqltype.Bool, sqltype.Text, sqltype.Int8, sqltype.Int8} {
		arg := n.Args[i]
		expected, ok := v.LookupType(typ)
		if !ok || arg == nil || arg.ResultUsage().Type != expected { return fmt.Errorf("stuntgres: invalid verify_heapam argument") }
	}
	current, _, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil { return err }
	if err := extension.ValidateVerifyHeapamIdentity(v, current, n.Ref); err != nil { return err }
	shape := n.Usage.Type
	if shape == nil || shape.ID() != 2249 || !shape.IsComposite() || shape.CompositeLen() != 4 { return fmt.Errorf("stuntgres: invalid verify_heapam OUT shape") }
	for i, name := range [...]string{"blkno", "offnum", "attnum", "msg"} {
		member := shape.CompositeMember(i)
		expected, ok := v.LookupType(([...]sqltype.ID{sqltype.Int8, sqltype.Int4, sqltype.Int4, sqltype.Text})[i])
		if !ok || member.Attribute != int32(i+1) || member.Name != name || member.Usage.Type != expected {
			return fmt.Errorf("stuntgres: invalid verify_heapam OUT shape")
		}
	}
	return nil
}
