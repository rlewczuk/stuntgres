package tablecmds

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// Isolated Restore calls this after every declaration path has constructed its
// real FK, including NOT VALID declarations already installed by ordinary ALTER.
// Logical names select existing definitions; normal provider validation remains
// mandatory, independently of whether saved data receives a snapshot waiver.
func applyRestoredForeignKeyModes(e *core.Engine, stage *core.CatalogStage, records []stored.ForeignKeyModeReplay) (map[ids.OID]bool, error) {
	if len(records) == 0 { return nil, nil }
	read := e.StageRead(stage)
	type ownerReplay struct {
		source *catalog.Relation
		positions map[string]int
		next *catalog.Relation
	}
	owners := make(map[[2]string]ownerReplay)
	var changed []*catalog.Relation
	selected := make(map[ids.OID]bool)
	masks := make(map[ids.OID]uint8)
	for _, record := range records {
		if err := record.Validate(); err != nil { return nil, err }
		name := [2]string{record.OwnerSchema, record.OwnerTable}
		owner, found := owners[name]
		if !found {
			namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: record.OwnerSchema})
			address := read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: record.OwnerTable})
			relation, ok := read.Relation(address)
			if !ok { return nil, fmt.Errorf("stuntgres: missing foreign-key mode owner") }
			owner.source = relation
			owner.positions = make(map[string]int, len(relation.Constraints.ForeignKeys))
			for i, key := range relation.Constraints.ForeignKeys { owner.positions[key.Name] = i }
			owners[name] = owner
		}
		index, found := owner.positions[record.Constraint]
		if !found { return nil, fmt.Errorf("stuntgres: missing foreign-key mode constraint") }
		key := owner.source.Constraints.ForeignKeys[index]
		if key.TriggerOIDs[record.Slot] == 0 { return nil, ids.ErrCatalogReplaced }
		if _, ok := read.Relation(key.Target); !ok { return nil, ids.ErrCatalogReplaced }
		bit := uint8(1) << record.Slot
		if masks[key.OID]&bit != 0 { return nil, fmt.Errorf("stuntgres: duplicate foreign-key mode slot") }
		masks[key.OID] |= bit
		mode := catalog.TriggerMode(record.Mode[0])
		if key.TriggerMode(record.Slot) == mode { continue }
		if owner.next == nil {
			next := *owner.source
			next.Constraints.ForeignKeys = slices.Clone(owner.source.Constraints.ForeignKeys)
			owner.next = &next
			owners[name] = owner
			changed = append(changed, owner.next)
		}
		owner.next.Constraints.ForeignKeys[index].TriggerModes[record.Slot] = mode
	}
	for oid, mask := range masks {
		if mask != 15 { return nil, fmt.Errorf("stuntgres: incomplete foreign-key mode snapshot") }
		selected[oid] = true
	}
	// Group validation precedes any stage replacement. Default-O snapshots
	// still select their exact FK identities without replacing any payload.
	for _, next := range changed {
		if err := stage.ReplaceDescriptor(next, false, false); err != nil { return nil, err }
	}
	return selected, nil
}
