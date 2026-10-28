package stored

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ForeignKeyModeRecords captures all four slots in allocation-independent order.
// The writer attaches the seal after every ordinary SQL section has completed.
func ForeignKeyModeRecords(ctx context.Context, read catalog.Read) ([]ForeignKeyModeReplay, error) {
	var records []ForeignKeyModeReplay
	for fact := range catalog.ForeignKeyTriggerFacts(read) {
		if err := ctx.Err(); err != nil { return nil, err }
		owner := read.MustRelation(fact.Owner)
		namespace := read.MustNamespace(owner.Namespace())
		if namespace.Temporary != (ids.SessionToken{}) { continue }
		records = append(records, ForeignKeyModeReplay{
			OwnerSchema: namespace.Name(), OwnerTable: owner.Name(),
			Constraint: fact.Constraint.Name, Slot: fact.Slot, Mode: fact.Mode.String(),
		})
	}
	slices.SortFunc(records, func(a,b ForeignKeyModeReplay) int {
		if a.OwnerSchema != b.OwnerSchema { return strings.Compare(a.OwnerSchema,b.OwnerSchema) }
		if a.OwnerTable != b.OwnerTable { return strings.Compare(a.OwnerTable,b.OwnerTable) }
		if a.Constraint != b.Constraint { return strings.Compare(a.Constraint,b.Constraint) }
		return a.Slot-b.Slot
	})
	return records, nil
}
