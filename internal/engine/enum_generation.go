package engine

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
)

// Engine.mu and statement admission are held. Reuse the catalog's established
// copy-on-change datum remapper for private writes and retained owner snapshots.
// Logical row identities, end roots and commit frontiers are not rewritten.
func enumReownVersions(next *catalog.State, versions []storage.RowVersion) ([]storage.RowVersion, error) {
	if len(versions) == 0 {
		return versions, nil
	}
	address := ids.ObjectAddress{}
	stores := map[ids.ObjectAddress]*storage.Relation{address: {Versions: versions}}
	if err := reownCatalogScalarTypes(next, stores, nil); err != nil {
		return nil, err
	}
	return stores[address].Versions, nil
}

func (e *Engine) adoptEnumGenerationsLocked(scope *StatementScope, changes *catalogChanges, stage *CatalogStage) error {
	next := e.stageRead(stage).State()
	for a, rebound := range stage.rowShapes {
		consumer, ok := stage.Get(a).(*catalog.Relation)
		if !ok {
			continue
		}
		previous, ok := stage.Base.Relation(a)
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
		shape, exists := changes.Storage[a]
		if !exists {
			shape = catalogStorageChange{Original: rebound.original, Root: rebound.original.Versions, Ends: rebound.observedEnds, Generation: rebound.generation}
		}
		if shape.Replacement != rebound.replacement {
			shape.Truncated = false
		}
		shape.Replacement, shape.Relation = rebound.replacement, consumer
		if shape.Fills != nil {
			shape.Fills = maps.Clone(shape.Fills)
			for attribute, value := range shape.Fills {
				target, _ := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(value.Type().ID())})
				if target != nil && target.SQLType != value.Type() {
					v, err := typedef.ReownDomainDatum(value, target.SQLType, nil, nil, nil)
					if err != nil {
						return err
					}
					shape.Fills[attribute] = v
				}
			}
		}
		if stableIsolation(scope.owner.Isolation) {
			versions := shape.Snapshot
			if !shape.HasSnapshot {
				if root, ok := scope.owner.Roots[a]; ok {
					versions = root.Versions
					shape.HasSnapshot = true
				}
			}
			if shape.HasSnapshot {
				var err error
				shape.Snapshot, err = enumReownVersions(next, versions)
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
		deletes = projectDeletions(deletes, shape.Replacement)
		scope.stageProjectedColumnRoots(a, writes, deletes)
		changes.Storage[a] = shape
	}
	return nil
}
