package engine

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Stage observations describe the exact private entry roots. Outer publication
// instead fences their committed predecessors, never private-vs-committed pointer
// equality and never another execution of the selected CHECK.
func (e *Engine) captureDomainStageLocked(stage *CatalogStage, changes *catalogChanges, current *catalog.State) error {
	if fence := stage.domainValidation; fence != nil {
		stores := make(map[ids.ObjectAddress]*relationStorage, len(fence.uses))
		for _, use := range fence.uses {
			stores[use.relation.Address()] = e.stageStorageLocked(stage, use.relation.Address())
		}
		if !fence.current(e.catalog.read(current), stores) {
			return catalog.Conflict{}
		}
		committed, err := domainUsesAt(e.catalog.read(e.catalog.state), fence.target, fence.path)
		if committed == nil {
			return err
		}
		// A prior own DROP may hide a committed stored container. Its presence
		// is an observed predecessor edge, not another validation blocker.
		committed.allowContainers = true
		for i := range committed.uses {
			use := &committed.uses[i]
			use.storage = e.storage.relations[use.relation.Address()]
			if use.storage == nil {
				return catalog.Conflict{}
			}
			use.versions = use.storage.versions[:len(use.storage.versions):len(use.storage.versions)]
			use.ends, use.generation = use.storage.ends, use.storage.generation
		}
		changes.domainValidations = append(changes.domainValidations, committed)
	}
	if len(stage.domainIndexRoots) != 0 {
		changes.domainIndexRoots = maps.Clone(changes.domainIndexRoots)
		if changes.domainIndexRoots == nil {
			changes.domainIndexRoots = make(map[ids.ObjectAddress]*logicalIndexRoot)
		}
		for address, root := range stage.domainIndexRoots {
			if e.domainStageIndexRootLocked(stage, address) != root {
				return catalog.Conflict{}
			}
			changes.domainIndexRoots[address] = e.storage.indexRoots[address]
		}
	}
	return nil
}

func (e *Engine) domainStageIndexRootLocked(stage *CatalogStage, address ids.ObjectAddress) *logicalIndexRoot {
	if stage.authSession != nil && stage.authSession.transactions.retainsTransaction() {
		if changes := stage.authSession.transactions.catalog; changes != nil {
			if root := changes.uniqueRoots[address]; root != nil {
				return root
			}
		}
	}
	return e.storage.indexRoots[address]
}

func (scope *StatementScope) reownDomainOverlayRows(stage *CatalogStage, changes *catalogChanges) error {
	if stage.domainAlter == nil {
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
		original, target := relationStorage{versions: writes}, relationStorage{versions: mapped}
		rebound, err := reownDomainIndexOutputs(scope.op.Context(), stage.Base.State(), next, &original, &target)
		if err != nil {
			return err
		}
		scope.stageProjectedColumnRoots(address, rebound.versions, projectDeletions(deletes, rowShape.replacement))
		shape := changes.storage[address]
		if len(shape.fills) != 0 {
			shape.fills = maps.Clone(shape.fills)
			for attribute, value := range shape.fills {
				target := next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(value.Type().ID())})
				shape.fills[attribute], err = reownDomainDatum(value, target.SQLType, nil, next.ScalarImports(), next.ACLItemImport())
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
				shape.snapshot, shape.hasSnapshot = snapshot.versions, true
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
