package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// TriggerCatalogDefinition borrows immutable slices for the synchronous visit.
// No executable recipe or analysis authority crosses the runtime boundary.
type TriggerCatalogDefinition struct {
	OID, Relation, Function, Parent ids.OID
	Name string
	Type int16
	Attributes []int32
	Arguments []string
	Qual sqltype.Value
}

func VisitTriggerCatalog(ctx context.Context, read catalog.Read, owner *catalog.Relation, typ *sqltype.Descriptor, visit func(TriggerCatalogDefinition) error) error {
	if err := ctx.Err(); err != nil { return err }
	if read.Owner() == nil || read.State() == nil || owner == nil || read.Get(owner.Address()) != owner { return fmt.Errorf("stuntgres: foreign trigger owner") }
	captured, ok := read.LookupType(sqltype.PgNodeTree)
	if !ok || captured != typ { return fmt.Errorf("stuntgres: foreign trigger tree type") }
	if owner.Triggers == nil { return nil }
	list, ok := owner.Triggers.(TriggerList)
	if !ok { return fmt.Errorf("stuntgres: invalid trigger payload") }
	if err := list.Validate(read, owner); err != nil { return err }
	for i := range list {
		p := list[i].parts
		var flags int16
		if p.Level == TriggerRow { flags |= 1 }
		if p.Timing == TriggerBefore { flags |= 2 }
		if p.Events&TriggerInsert != 0 { flags |= 4 }
		if p.Events&TriggerDelete != 0 { flags |= 8 }
		if p.Events&TriggerUpdate != 0 { flags |= 16 }
		if p.Events&TriggerTruncate != 0 { flags |= 32 }
		qual, err := triggerTree(ctx, typ, &list[i])
		if err != nil { return err }
		if err := visit(TriggerCatalogDefinition{OID:p.OID, Relation:p.Relation.Address.ObjectID, Function:p.Procedure.Address.ObjectID, Parent:p.Parent.ObjectID, Name:p.Name, Type:flags, Attributes:p.UpdateColumns, Arguments:p.Arguments, Qual:qual}); err != nil { return err }
	}
	return ctx.Err()
}
