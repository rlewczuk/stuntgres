package engine

import (
	"fmt"
	"maps"
	"slices"
)

// Project a DROP shape through committed, private-write and retained snapshot
// roots. The original publication preconditions remain in shape.
func projectColumnShape(scope *StatementScope, old, next *relationDescriptor, store *relationStorage, shape catalogStorageChange, snapshot transactionRoot, snapshotExists bool) (catalogStorageChange, []rowVersion, *privateDeletionRoot, error) {
	writes, deletes := scope.columnProjectionRoots(old.address)
	if slices.Equal(old.attributes, next.attributes) {
		shape.relation = next
		return shape, writes, deletes, nil
	}
	if store.generation == ^uint64(0) {
		return shape, nil, nil, fmt.Errorf("stuntgres: storage generation exhausted")
	}
	shape.fills = maps.Clone(shape.fills)
	for attribute := range shape.fills {
		if !next.hasAttribute(attribute) {
			delete(shape.fills, attribute)
		}
	}
	projection, err := newColumnProjection(old, next, shape.fills)
	if err != nil {
		return shape, nil, nil, err
	}
	versions, err := projection.versions(scope.op.Context(), store.versions)
	if err != nil {
		return shape, nil, nil, err
	}
	shape.replacement = &relationStorage{versions: versions, ends: store.ends, generation: store.generation + 1}
	writes, err = projection.versions(scope.op.Context(), writes)
	if err != nil {
		return shape, nil, nil, err
	}
	deletes = projectDeletions(deletes, shape.replacement)
	if stableIsolation(scope.owner.isolation) && snapshotExists && !shape.rewritten {
		if slices.Equal(snapshot.relation.attributes, old.attributes) {
			shape.snapshot, err = projection.versions(scope.op.Context(), snapshot.versions)
		} else {
			shape.snapshot, err = projectVersions(scope.op.Context(), snapshot.versions, snapshot.relation, next, shape.fills)
		}
		if err != nil {
			return shape, nil, nil, err
		}
		shape.hasSnapshot = true
	}
	shape.relation = next
	return shape, writes, deletes, nil
}

// Nested cascades may have already projected this statement's private roots.
func (scope *StatementScope) columnProjectionRoots(address ObjectAddress) ([]rowVersion, *privateDeletionRoot) {
	if scope.projectedRoots {
		return scope.stage.rows[address], scope.stage.deletes[address]
	}
	return scope.ownWrites[address], scope.ownDeletes[address]
}

func (scope *StatementScope) stageProjectedColumnRoots(address ObjectAddress, writes []rowVersion, deletes *privateDeletionRoot) {
	if !scope.projectedRoots {
		scope.stage.rows = maps.Clone(scope.ownWrites)
		scope.stage.deletes = maps.Clone(scope.ownDeletes)
	}
	scope.projectedRoots = true
	if len(scope.ownWrites) != 0 {
		if scope.stage.rows == nil {
			scope.stage.rows = make(map[ObjectAddress][]rowVersion)
		}
		scope.stage.rows[address] = writes
		scope.stage.replacing = true
	}
	if scope.stage.deletes == nil {
		scope.stage.deletes = make(map[ObjectAddress]*privateDeletionRoot)
	}
	scope.stage.deletes[address] = deletes
}

func (scope *StatementScope) stagePrivateColumnShape(stage *CatalogStage, changes *catalogChanges, old, next *relationDescriptor) error {
	e := stage.owner
	e.mu.Lock()
	original := e.storage[old.address]
	store := e.privateStorageLocked(changes, old.address)
	shape, exists := changes.storage[old.address]
	if original == nil || store == nil {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: relation storage is missing")
	}
	if !exists {
		storageOwner, ok := stage.base.state.descriptors[old.address].(*relationDescriptor)
		if !ok {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: relation storage descriptor is missing")
		}
		shape = catalogStorageChange{original: original, root: original.versions, ends: original.ends, generation: original.generation, replacement: store, relation: storageOwner}
	}
	snapshot, snapshotExists := scope.owner.roots[old.address]
	e.mu.Unlock()
	shape, writes, deletes, err := projectColumnShape(scope, shape.relation, next, store, shape, snapshot, snapshotExists)
	if err != nil {
		return err
	}
	if err = stage.Replace(next); err != nil {
		return err
	}
	e.mu.Lock()
	err = e.evolveStageIndexesLocked(stage, old.address, nil)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	changes.storage[old.address] = shape
	if err = prepareAlterUniqueRoots(scope.op.Context(), changes, next, shape.replacement, nil); err != nil {
		return err
	}
	if shape.replacement != store {
		scope.stageProjectedColumnRoots(old.address, writes, deletes)
	}
	return nil
}
