package engine

import "maps"

// Stage observations describe the exact private entry roots. Outer publication
// instead fences their committed predecessors, never private-vs-committed pointer
// equality and never another execution of the selected CHECK.
func (e *Engine) captureDomainStageLocked(stage *CatalogStage, changes *catalogChanges, current *catalogState) error {
	if fence := stage.domainValidation; fence != nil {
		stores := make(map[ObjectAddress]*relationStorage, len(fence.uses))
		for _, use := range fence.uses {
			stores[use.relation.address] = e.stageStorageLocked(stage, use.relation.address)
		}
		if !fence.current(CatalogRead{e, current}, stores) {
			return CatalogConflict{}
		}
		committed, err := domainUsesAt(CatalogRead{e, e.state}, fence.target, fence.path)
		if committed == nil {
			return err
		}
		// A prior own DROP may hide a committed stored container. Its presence
		// is an observed predecessor edge, not another validation blocker.
		committed.allowContainers = true
		for i := range committed.uses {
			use := &committed.uses[i]
			use.storage = e.storage[use.relation.address]
			if use.storage == nil {
				return CatalogConflict{}
			}
			use.versions = use.storage.versions[:len(use.storage.versions):len(use.storage.versions)]
			use.ends, use.generation = use.storage.ends, use.storage.generation
		}
		changes.domainValidations = append(changes.domainValidations, committed)
	}
	if len(stage.domainIndexRoots) != 0 {
		changes.domainIndexRoots = maps.Clone(changes.domainIndexRoots)
		if changes.domainIndexRoots == nil {
			changes.domainIndexRoots = make(map[ObjectAddress]*logicalIndexRoot)
		}
		for address, root := range stage.domainIndexRoots {
			if e.domainStageIndexRootLocked(stage, address) != root {
				return CatalogConflict{}
			}
			changes.domainIndexRoots[address] = e.indexRoots[address]
		}
	}
	return nil
}

func (e *Engine) domainStageIndexRootLocked(stage *CatalogStage, address ObjectAddress) *logicalIndexRoot {
	if stage.authSession != nil && stage.authSession.transactions.retainsTransaction() {
		if changes := stage.authSession.transactions.catalog; changes != nil {
			if root := changes.uniqueRoots[address]; root != nil {
				return root
			}
		}
	}
	return e.indexRoots[address]
}

func (scope *StatementScope) reownDomainOverlayRows(stage *CatalogStage, changes *catalogChanges) error {
	if stage.domainAlter == nil {
		return nil
	}
	e := scope.op.session.Engine()
	e.mu.Lock()
	next := e.privateCatalogLocked(changes).state
	e.mu.Unlock()
	for address, rowShape := range stage.rowShapes {
		writes, deletes := scope.columnProjectionRoots(address)
		mapped, err := enumReownVersions(next, writes)
		if err != nil {
			return err
		}
		original, target := relationStorage{versions: writes}, relationStorage{versions: mapped}
		rebound, err := reownDomainIndexOutputs(scope.op.Context(), stage.base.state, next, &original, &target)
		if err != nil {
			return err
		}
		scope.stageProjectedColumnRoots(address, rebound.versions, projectDeletions(deletes, rowShape.replacement))
		shape := changes.storage[address]
		if len(shape.fills) != 0 {
			shape.fills = maps.Clone(shape.fills)
			for attribute, value := range shape.fills {
				target := next.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(value.Type().ID())}].(*typeDescriptor)
				shape.fills[attribute], err = reownDomainDatum(value, target.typ, nil, next.scalarImports, next.aclItemImport)
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
