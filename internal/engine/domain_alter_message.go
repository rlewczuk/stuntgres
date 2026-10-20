package engine

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
)

// Stage observations describe the exact private entry roots. Outer publication
// instead fences their committed predecessors, never private-vs-committed pointer
// equality and never another execution of the selected CHECK.
func (e *Engine) captureDomainStageLocked(stage *CatalogStage, changes *catalogChanges, current *catalog.State) error {
	if fence := stage.domainAlter.ValidationFence(); fence != nil {
		stores := make(map[ids.ObjectAddress]*storage.Relation, len(fence.Uses))
		for _, use := range fence.Uses {
			stores[use.Relation.Address()] = e.stageStorageLocked(stage, use.Relation.Address())
		}
		if !fence.Current(e.catalog.read(current), stores) {
			return catalog.Conflict{}
		}
		committed, err := typedef.DomainUsesAt(e.catalog.read(e.catalog.state), fence.Target, fence.Path)
		if committed == nil {
			return err
		}
		// A prior own DROP may hide a committed stored container. Its presence
		// is an observed predecessor edge, not another validation blocker.
		committed.AllowContainers = true
		for i := range committed.Uses {
			use := &committed.Uses[i]
			use.Storage = e.storage.Relations[use.Relation.Address()]
			if use.Storage == nil {
				return catalog.Conflict{}
			}
			use.Versions = use.Storage.Versions[:len(use.Storage.Versions):len(use.Storage.Versions)]
			use.Ends, use.Generation = use.Storage.Ends, use.Storage.Generation
		}
		journal := domainWritable(changes)
		journal.Validations = append(journal.Validations, committed)
	}
	if len(stage.domainAlter.AlteredIndexRoots()) != 0 {
		journal := domainWritable(changes)
		journal.IndexRoots = maps.Clone(journal.IndexRoots)
		if journal.IndexRoots == nil {
			journal.IndexRoots = make(map[ids.ObjectAddress]*storage.IndexRoot)
		}
		for address, root := range stage.domainAlter.AlteredIndexRoots() {
			if e.domainStageIndexRootLocked(stage, address) != root {
				return catalog.Conflict{}
			}
			journal.IndexRoots[address] = e.storage.IndexRoots[address]
		}
	}
	return nil
}

func (e *Engine) domainStageIndexRootLocked(stage *CatalogStage, address ids.ObjectAddress) *storage.IndexRoot {
	if stage.authSession != nil && stage.authSession.transactions.retainsTransaction() {
		if changes := stage.authSession.transactions.catalog; changes != nil {
			if root := journalIndexesOrZero(changes).uniqueRoots[address]; root != nil {
				return root
			}
		}
	}
	return e.storage.IndexRoots[address]
}

func (scope *StatementScope) reownDomainOverlayRows(stage *CatalogStage, changes *catalogChanges) error {
	if stage.domainAlter.AlterContext() == nil {
		return nil
	}
	e := scope.op.session.Engine()
	e.mu.Lock()
	next := e.privateCatalogLocked(changes).State()
	e.mu.Unlock()
	for address, rowShape := range stage.rowShapes {
		writes, deletes := scope.columnProjectionRoots(address)
		mapped, err := enumReownVersions(next, writes)
		if err != nil {
			return err
		}
		original, target := storage.Relation{Versions: writes}, storage.Relation{Versions: mapped}
		rebound, err := reownDomainIndexOutputs(scope.op.Context(), stage.Base.State(), next, &original, &target)
		if err != nil {
			return err
		}
		scope.stageProjectedColumnRoots(address, rebound.Versions, projectDeletions(deletes, rowShape.replacement))
		shape := changes.storage[address]
		if len(shape.fills) != 0 {
			shape.fills = maps.Clone(shape.fills)
			for attribute, value := range shape.fills {
				target := next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(value.Type().ID())})
				shape.fills[attribute], err = typedef.ReownDomainDatum(value, target.SQLType, nil, next.ScalarImports(), next.ACLItemImport())
				if err != nil {
					return err
				}
			}
		}
		if stableIsolation(scope.owner.isolation) && !shape.hasSnapshot {
			scope.owner.mu.Lock()
			snapshot, exists := scope.owner.roots[address]
			scope.owner.mu.Unlock()
			if exists {
				shape.snapshot, shape.hasSnapshot = snapshot.Versions, true
			}
		}
		if shape.hasSnapshot {
			shape.snapshot, err = enumReownVersions(next, shape.snapshot)
			if err != nil {
				return err
			}
		}
		changes.storage[address] = shape
	}
	return nil
}
