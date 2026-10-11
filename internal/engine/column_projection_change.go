package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// Project a DROP shape through committed, private-write and retained snapshot
// roots. The original publication preconditions remain in shape.
func projectColumnShape(scope *StatementScope, old, next *relationDescriptor, store *relationStorage, shape catalogStorageChange, snapshot transactionRoot, snapshotExists bool) (catalogStorageChange, []rowVersion, *privateDeletionRoot, error) {
	writes, deletes := scope.columnProjectionRoots(old.address)
	return projectColumnShapeRoots(scope.op.Context(), stableIsolation(scope.owner.isolation), old, next, store, shape, snapshot, snapshotExists, writes, deletes)
}

func projectColumnShapeRoots(ctx context.Context, stable bool, old, next *relationDescriptor, store *relationStorage, shape catalogStorageChange, snapshot transactionRoot, snapshotExists bool, writes []rowVersion, deletes *privateDeletionRoot) (catalogStorageChange, []rowVersion, *privateDeletionRoot, error) {
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
	versions, err := projection.versions(ctx, store.versions)
	if err != nil {
		return shape, nil, nil, err
	}
	shape.replacement = &relationStorage{versions: versions, ends: store.ends, generation: store.generation + 1}
	shape.truncated = false
	writes, err = projection.versions(ctx, writes)
	if err != nil {
		return shape, nil, nil, err
	}
	deletes = projectDeletions(deletes, shape.replacement)
	if stable && snapshotExists && !shape.rewritten {
		if slices.Equal(snapshot.relation.attributes, old.attributes) {
			shape.snapshot, err = projection.versions(ctx, snapshot.versions)
		} else {
			shape.snapshot, err = projectVersions(ctx, snapshot.versions, snapshot.relation, next, shape.fills)
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
	writes, deletes := scope.columnProjectionRoots(old.address)
	stage.owner.mu.Lock()
	snapshot, snapshotExists := scope.owner.roots[old.address]
	stage.owner.mu.Unlock()
	writes, deletes, projected, err := stagePrivateColumnShape(scope.op.Context(), stage, changes, old, next, writes, deletes, snapshot, snapshotExists, stableIsolation(scope.owner.isolation))
	if err == nil && projected {
		scope.stageProjectedColumnRoots(old.address, writes, deletes)
	}
	return err
}

func stagePrivateColumnShape(ctx context.Context, stage *CatalogStage, changes *catalogChanges, old, next *relationDescriptor, writes []rowVersion, deletes *privateDeletionRoot, snapshot transactionRoot, snapshotExists, stable bool) ([]rowVersion, *privateDeletionRoot, bool, error) {
	e := stage.owner
	e.mu.Lock()
	original := e.storage[old.address]
	store := e.privateStorageLocked(changes, old.address)
	shape, exists := changes.storage[old.address]
	if store == nil || original == nil && !exists {
		e.mu.Unlock()
		return nil, nil, false, fmt.Errorf("stuntgres: relation storage is missing")
	}
	if !exists {
		storageOwner, ok := stage.base.state.descriptors[old.address].(*relationDescriptor)
		if !ok {
			e.mu.Unlock()
			return nil, nil, false, fmt.Errorf("stuntgres: relation storage descriptor is missing")
		}
		shape = catalogStorageChange{original: original, root: original.versions, ends: original.ends, generation: original.generation, replacement: store, relation: storageOwner}
	}
	e.mu.Unlock()
	shape, writes, deletes, err := projectColumnShapeRoots(ctx, stable, shape.relation, next, store, shape, snapshot, snapshotExists, writes, deletes)
	if err != nil {
		return nil, nil, false, err
	}
	if err = stage.Replace(next); err != nil {
		return nil, nil, false, err
	}
	e.mu.Lock()
	err = e.evolveStageIndexesLocked(stage, old.address, nil)
	if err == nil {
		for address, descriptor := range stage.descriptors {
			index, ok := descriptor.(*relationDescriptor)
			if ok && index.sqlIndex != nil && index.sqlIndex.parent == old.address {
				e.catalogChangeLocked(changes, address, index)
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return nil, nil, false, err
	}
	changes.storage[old.address] = shape
	if err = prepareAlterUniqueRoots(ctx, changes, stage.base, next, shape.replacement, nil, false, nil); err != nil {
		return nil, nil, false, err
	}
	return writes, deletes, shape.replacement != store, nil
}

// Generic utility stages already project their baseline storage. Reuse the
// canonical column projection for earlier own writes, deletions and stable
// snapshots without projecting that physical baseline a second time.
func (scope *StatementScope) projectCatalogOverlayShapes(stage *CatalogStage, changes *catalogChanges) error {
	for address, rowShape := range stage.rowShapes {
		old, oldOK := stage.base.state.descriptors[address].(*relationDescriptor)
		next, nextOK := stage.descriptors[address].(*relationDescriptor)
		if !oldOK || !nextOK || slices.Equal(old.attributes, next.attributes) {
			continue
		}
		shape := changes.storage[address]
		shape.fills = maps.Clone(shape.fills)
		for attribute := range shape.fills {
			if !next.hasAttribute(attribute) {
				delete(shape.fills, attribute)
			}
		}
		projection, err := newColumnProjection(old, next, shape.fills)
		if err != nil {
			return err
		}
		writes, deletes := scope.columnProjectionRoots(address)
		writes, err = projection.versions(scope.op.Context(), writes)
		if err != nil {
			return err
		}
		scope.stageProjectedColumnRoots(address, writes, projectDeletions(deletes, rowShape.replacement))
		stage.owner.mu.Lock()
		snapshot, snapshotExists := scope.owner.roots[address]
		stage.owner.mu.Unlock()
		if stableIsolation(scope.owner.isolation) && snapshotExists && !shape.rewritten {
			shape.snapshot, err = projectVersions(scope.op.Context(), snapshot.versions, snapshot.relation, next, shape.fills)
			if err != nil {
				return err
			}
			shape.hasSnapshot = true
		}
		changes.storage[address] = shape
	}
	return nil
}
