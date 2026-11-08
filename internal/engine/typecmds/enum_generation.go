package typecmds

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
)

func adoptEnumGenerationsLocked(e *core.Engine, scope *core.StatementScope, changes *session.Journal, stage *core.CatalogStage) error {
	next := e.StageRead(stage).State()
	for a, rebound := range stage.RowShapes {
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
			shape = session.StorageChange{Original: rebound.Original, Root: rebound.Original.Versions, Ends: rebound.ObservedEnds, ObservedVisibility: rebound.ObservedVisibility, ObservedAuxiliary: rebound.ObservedAuxiliary, Generation: rebound.Generation}
		}
		if shape.Replacement != rebound.Replacement {
			shape.Truncated = false
		}
		if exists && shape.Replacement == shape.Original && rebound.PreservesVisibility {
			shape.ObservedVisibility = rebound.ObservedVisibility
			shape.ObservedAuxiliary = rebound.ObservedAuxiliary
		}
		shape.Replacement, shape.Relation = rebound.Replacement, consumer
		// Reowning seeded this stage with the existing chain's provenance.
		shape.PreservesVisibility = rebound.PreservesVisibility
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
		if scope.Owner.Isolation.Stable() {
			versions := shape.Snapshot
			if !shape.HasSnapshot {
				if root, ok := scope.Owner.Roots[a]; ok {
					versions = root.Versions
					shape.HasSnapshot = true
				}
			}
			if shape.HasSnapshot {
				var err error
				shape.Snapshot, err = stored.ReownVersions(next, versions)
				if err != nil {
					return err
				}
			}
		}
		writes, deletes := scope.ColumnProjectionRoots(a)
		originalWrites := writes
		writes, err := stored.ReownVersions(next, writes)
		if err != nil {
			return err
		}
		original, target := storage.Relation{Versions: originalWrites}, storage.Relation{Versions: writes}
		reboundWrites, err := index.ReownIndexOutputs(scope.Op.Context(), stage.Base.State(), next, &original, &target)
		if err != nil {
			return err
		}
		writes = reboundWrites.Versions
		deletes = session.ProjectDeletions(deletes, shape.Replacement)
		scope.StageProjectedColumnRoots(a, writes, deletes)
		changes.Storage[a] = shape
	}
	// Enum DDL records descriptors manually, so it must also adopt the real
	// transported candidate and root into its cloned pending journal.
	for address, candidate := range stage.IndexesOrZero().Candidates {
		owned := index.JournalIndexes(changes)
		if owned.Candidates == nil {
			owned.Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
		}
		if owned.UniqueRoots == nil {
			owned.UniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot)
		}
		owned.Candidates[address], owned.UniqueRoots[address] = candidate, candidate.Root
	}
	return nil
}
