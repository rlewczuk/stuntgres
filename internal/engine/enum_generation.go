package engine

import (
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Engine.mu and statement admission are held. Reuse the catalog's established
// copy-on-change datum remapper for private writes and retained owner snapshots.
// Logical row identities, end roots and commit frontiers are not rewritten.
func enumReownVersions(next *catalogState, versions []rowVersion) ([]rowVersion, error) {
	if len(versions) == 0 {
		return versions, nil
	}
	address := ids.ObjectAddress{}
	stores := map[ids.ObjectAddress]*relationStorage{address: {versions: versions}}
	if err := reownCatalogScalarTypes(next, stores, nil); err != nil {
		return nil, err
	}
	return stores[address].versions, nil
}

func (e *Engine) adoptEnumGenerationsLocked(scope *StatementScope, changes *catalogChanges, stage *CatalogStage) error {
	next := e.stageRead(stage).State()
	for a, rebound := range stage.rowShapes {
		consumer, ok := stage.descriptors[a].(*relationDescriptor)
		if !ok {
			continue
		}
		previous, ok := stage.base.Relation(a)
		if !ok {
			continue
		}
		changed := false
		for i, c := range consumer.Columns {
			changed = changed || c.Usage.Type != previous.Columns[i].Usage.Type
		}
		if !changed {
			continue
		}
		shape, exists := changes.storage[a]
		if !exists {
			shape = catalogStorageChange{original: rebound.original, root: rebound.original.versions, ends: rebound.observedEnds, generation: rebound.generation}
		}
		if shape.replacement != rebound.replacement {
			shape.truncated = false
		}
		shape.replacement, shape.relation = rebound.replacement, consumer
		if shape.fills != nil {
			shape.fills = maps.Clone(shape.fills)
			for attribute, value := range shape.fills {
				target, _ := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(value.Type().ID())})
				if target != nil && target.SQLType != value.Type() {
					v, err := reownDomainDatum(value, target.SQLType, nil, nil, nil)
					if err != nil {
						return err
					}
					shape.fills[attribute] = v
				}
			}
		}
		if stableIsolation(scope.owner.isolation) {
			versions := shape.snapshot
			if !shape.hasSnapshot {
				if root, ok := scope.owner.roots[a]; ok {
					versions = root.versions
					shape.hasSnapshot = true
				}
			}
			if shape.hasSnapshot {
				var err error
				shape.snapshot, err = enumReownVersions(next, versions)
				if err != nil {
					return err
				}
			}
		}
		writes, deletes := scope.columnProjectionRoots(a)
		writes, err := enumReownVersions(next, writes)
		if err != nil {
			return err
		}
		deletes = projectDeletions(deletes, shape.replacement)
		scope.stageProjectedColumnRoots(a, writes, deletes)
		changes.storage[a] = shape
	}
	return nil
}

// Immutable operation provenance: no SQL input callback reaches live session
// state or modifies a definition. Committed captures have no unsafe members.
func enumSafety(unsafe map[int64]bool) func(sqltype.ID, int64) bool {
	if len(unsafe) == 0 {
		return nil
	}
	return func(_ sqltype.ID, member int64) bool { return !unsafe[member] }
}
