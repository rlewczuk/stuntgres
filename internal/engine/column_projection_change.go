package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Project a DROP shape through committed, private-write and retained snapshot
// roots. The original publication preconditions remain in shape.
func projectColumnShape(scope *StatementScope, old, next *catalog.Relation, store *storage.Relation, shape session.StorageChange, snapshot visibility.Root, snapshotExists bool) (session.StorageChange, []storage.RowVersion, *visibility.PrivateDeletes, error) {
	writes, deletes := scope.columnProjectionRoots(old.Address())
	return session.ProjectColumnShapeRoots(scope.op.Context(), scope.owner.Isolation.Stable(), old, next, store, shape, snapshot, snapshotExists, writes, deletes)
}

// Nested cascades may have already projected this statement's private roots.
func (scope *StatementScope) columnProjectionRoots(address ids.ObjectAddress) ([]storage.RowVersion, *visibility.PrivateDeletes) {
	if scope.projectedRoots {
		return scope.stage.rows[address], scope.stage.deletes[address]
	}
	return scope.ownWrites[address], scope.ownDeletes[address]
}

func (scope *StatementScope) stageProjectedColumnRoots(address ids.ObjectAddress, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) {
	if !scope.projectedRoots {
		scope.stage.rows = maps.Clone(scope.ownWrites)
		scope.stage.deletes = maps.Clone(scope.ownDeletes)
	}
	scope.projectedRoots = true
	if len(scope.ownWrites) != 0 {
		if scope.stage.rows == nil {
			scope.stage.rows = make(map[ids.ObjectAddress][]storage.RowVersion)
		}
		scope.stage.rows[address] = writes
		scope.stage.replacing = true
	}
	if scope.stage.deletes == nil {
		scope.stage.deletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes)
	}
	scope.stage.deletes[address] = deletes
}

func (scope *StatementScope) stagePrivateColumnShape(stage *CatalogStage, changes *session.Journal, old, next *catalog.Relation) error {
	return scope.stageColumnShape(stage, changes, old, next, false)
}

func (scope *StatementScope) stagePreparedColumnShape(stage *CatalogStage, changes *session.Journal, old, next *catalog.Relation) error {
	return scope.stageColumnShape(stage, changes, old, next, true)
}

func (scope *StatementScope) stageColumnShape(stage *CatalogStage, changes *session.Journal, old, next *catalog.Relation, metadataPrepared bool) error {
	writes, deletes := scope.columnProjectionRoots(old.Address())
	stage.owner.mu.Lock()
	snapshot, snapshotExists := scope.owner.Roots[old.Address()]
	stage.owner.mu.Unlock()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	writes, deletes, projected, err := stagePrivateColumnShapeMetadata(&evaluation, stage, changes, old, next, writes, deletes, snapshot, snapshotExists, scope.owner.Isolation.Stable(), metadataPrepared)
	if err == nil && projected {
		scope.stageProjectedColumnRoots(old.Address(), writes, deletes)
	}
	return err
}

func stagePrivateColumnShape(evaluation *exec.Context, stage *CatalogStage, changes *session.Journal, old, next *catalog.Relation, writes []storage.RowVersion, deletes *visibility.PrivateDeletes, snapshot visibility.Root, snapshotExists, stable bool) ([]storage.RowVersion, *visibility.PrivateDeletes, bool, error) {
	return stagePrivateColumnShapeMetadata(evaluation, stage, changes, old, next, writes, deletes, snapshot, snapshotExists, stable, false)
}

// The prepared path consumes the same physical projector after the immutable
// descriptor/index/lineage effects have been applied to this sole stage.
func stagePrivateColumnShapeMetadata(evaluation *exec.Context, stage *CatalogStage, changes *session.Journal, old, next *catalog.Relation, writes []storage.RowVersion, deletes *visibility.PrivateDeletes, snapshot visibility.Root, snapshotExists, stable, metadataPrepared bool) ([]storage.RowVersion, *visibility.PrivateDeletes, bool, error) {
	ctx := evaluation.Context
	e := stage.owner
	e.mu.Lock()
	original := e.storage.Relations[old.Address()]
	store := e.privateStorageLocked(changes, old.Address())
	shape, exists := changes.Storage[old.Address()]
	if store == nil || original == nil && !exists {
		e.mu.Unlock()
		return nil, nil, false, fmt.Errorf("stuntgres: relation storage is missing")
	}
	if !exists {
		storageOwner, ok := stage.Base.Relation(old.Address())
		if !ok {
			e.mu.Unlock()
			return nil, nil, false, fmt.Errorf("stuntgres: relation storage descriptor is missing")
		}
		shape = session.StorageChange{Original: original, Root: original.Versions, Ends: original.Ends, Generation: original.Generation, Replacement: store, Relation: storageOwner}
	}
	e.mu.Unlock()
	sourceWrites := writes
	shape, writes, deletes, err := session.ProjectColumnShapeRoots(ctx, stable, shape.Relation, next, store, shape, snapshot, snapshotExists, writes, deletes)
	if err != nil {
		return nil, nil, false, err
	}
	if !metadataPrepared {
		if err = stage.Replace(next); err != nil {
			return nil, nil, false, err
		}
	}
	e.mu.Lock()
	if !metadataPrepared {
		err = e.evolveStageIndexesLocked(stage, old.Address(), nil)
	}
	if err == nil {
		for address, descriptor := range stage.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if ok && index.SQLIndex != nil && index.SQLIndex.Parent == old.Address() {
				e.catalogChangeLocked(changes, address, index)
			}
		}
	}
	evaluation.Catalog = e.stageRead(stage)
	e.mu.Unlock()
	if err != nil {
		return nil, nil, false, err
	}
	changes.Storage[old.Address()] = shape
	var transported bool
	writes, transported, err = transportIndexLayouts(evaluation, stage, changes, next, store, shape.Replacement, sourceWrites, writes)
	if err != nil {
		return nil, nil, false, err
	}
	if err = index.PrepareAlterIndexRoots(evaluation, changes, stage.Base, next, shape.Replacement, nil, false); err != nil {
		return nil, nil, false, err
	}
	return writes, deletes, shape.Replacement != store || transported, nil
}

// Generic utility stages already project their baseline storage. Reuse the
// canonical column projection for earlier own writes, deletions and stable
// snapshots without projecting that physical baseline a second time.
func (scope *StatementScope) projectCatalogOverlayShapes(stage *CatalogStage, changes *session.Journal) error {
	for address, rowShape := range stage.rowShapes {
		old, oldOK := stage.Base.Relation(address)
		next, nextOK := stage.Get(address).(*catalog.Relation)
		if !oldOK || !nextOK || slices.Equal(old.Attributes, next.Attributes) {
			continue
		}
		shape := changes.Storage[address]
		shape.Fills = maps.Clone(shape.Fills)
		for attribute := range shape.Fills {
			if !next.HasAttribute(attribute) {
				delete(shape.Fills, attribute)
			}
		}
		projection, err := session.NewColumnProjection(old, next, shape.Fills)
		if err != nil {
			return err
		}
		writes, deletes := scope.columnProjectionRoots(address)
		writes, err = projection.Versions(scope.op.Context(), writes)
		if err != nil {
			return err
		}
		scope.stageProjectedColumnRoots(address, writes, session.ProjectDeletions(deletes, rowShape.replacement))
		stage.owner.mu.Lock()
		snapshot, snapshotExists := scope.owner.Roots[address]
		stage.owner.mu.Unlock()
		if scope.owner.Isolation.Stable() && snapshotExists && !shape.Rewritten {
			shape.Snapshot, err = session.ProjectVersions(scope.op.Context(), snapshot.Versions, snapshot.Relation, next, shape.Fills)
			if err != nil {
				return err
			}
			shape.HasSnapshot = true
		}
		changes.Storage[address] = shape
	}
	return nil
}
