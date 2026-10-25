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
	writes, deletes := scope.ColumnProjectionRoots(old.Address())
	return session.ProjectColumnShapeRoots(scope.Op.Context(), scope.Owner.Isolation.Stable(), old, next, store, shape, snapshot, snapshotExists, writes, deletes)
}

// Nested cascades may have already projected this statement's private roots.
func (scope *StatementScope) ColumnProjectionRoots(address ids.ObjectAddress) ([]storage.RowVersion, *visibility.PrivateDeletes) {
	if scope.projectedRoots {
		return scope.Stage.Rows[address], scope.Stage.Deletes[address]
	}
	return scope.OwnWrites[address], scope.OwnDeletes[address]
}

func (scope *StatementScope) StageProjectedColumnRoots(address ids.ObjectAddress, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) {
	if !scope.projectedRoots {
		scope.Stage.Rows = maps.Clone(scope.OwnWrites)
		scope.Stage.Deletes = maps.Clone(scope.OwnDeletes)
	}
	scope.projectedRoots = true
	if len(scope.OwnWrites) != 0 {
		if scope.Stage.Rows == nil {
			scope.Stage.Rows = make(map[ids.ObjectAddress][]storage.RowVersion)
		}
		scope.Stage.Rows[address] = writes
		scope.Stage.replacing = true
	}
	if scope.Stage.Deletes == nil {
		scope.Stage.Deletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes)
	}
	scope.Stage.Deletes[address] = deletes
}

func (scope *StatementScope) stagePrivateColumnShape(stage *CatalogStage, changes *session.Journal, old, next *catalog.Relation) error {
	return scope.StageColumnShape(stage, changes, old, next, false)
}

func stagePreparedColumnShape(scope *StatementScope, stage *CatalogStage, changes *session.Journal, old, next *catalog.Relation) error {
	return scope.StageColumnShape(stage, changes, old, next, true)
}

func (scope *StatementScope) StageColumnShape(stage *CatalogStage, changes *session.Journal, old, next *catalog.Relation, metadataPrepared bool) error {
	writes, deletes := scope.ColumnProjectionRoots(old.Address())
	stage.Owner.Mu.Lock()
	snapshot, snapshotExists := scope.Owner.Roots[old.Address()]
	stage.Owner.Mu.Unlock()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	writes, deletes, projected, err := stagePrivateColumnShapeMetadata(&evaluation, stage, changes, old, next, writes, deletes, snapshot, snapshotExists, scope.Owner.Isolation.Stable(), metadataPrepared)
	if err == nil && projected {
		scope.StageProjectedColumnRoots(old.Address(), writes, deletes)
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
	e := stage.Owner
	e.Mu.Lock()
	original := e.Storage.Relations[old.Address()]
	store := e.PrivateStorageLocked(changes, old.Address())
	shape, exists := changes.Storage[old.Address()]
	if store == nil || original == nil && !exists {
		e.Mu.Unlock()
		return nil, nil, false, fmt.Errorf("stuntgres: relation storage is missing")
	}
	if !exists {
		storageOwner, ok := stage.Base.Relation(old.Address())
		if !ok {
			e.Mu.Unlock()
			return nil, nil, false, fmt.Errorf("stuntgres: relation storage descriptor is missing")
		}
		shape = session.StorageChange{Original: original, Root: original.Versions, Ends: original.Ends, Generation: original.Generation, Replacement: store, Relation: storageOwner}
	}
	e.Mu.Unlock()
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
	e.Mu.Lock()
	if !metadataPrepared {
		err = e.EvolveStageIndexesLocked(stage, old.Address(), nil)
	}
	if err == nil {
		for address, descriptor := range stage.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if ok && index.SQLIndex != nil && index.SQLIndex.Parent == old.Address() {
				e.CatalogChangeLocked(changes, address, index)
			}
		}
	}
	evaluation.Catalog = e.StageRead(stage)
	e.Mu.Unlock()
	if err != nil {
		return nil, nil, false, err
	}
	changes.Storage[old.Address()] = shape
	var transported bool
	writes, transported, err = TransportIndexLayouts(evaluation, stage, changes, next, store, shape.Replacement, sourceWrites, writes)
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
	for address, rowShape := range stage.RowShapes {
		old, oldOK := stage.Base.Relation(address)
		next, nextOK := stage.Get(address).(*catalog.Relation)
		if !oldOK || !nextOK || slices.Equal(old.Attributes, next.Attributes) {
			continue
		}
		shape := changes.Storage[address]
		shape.MergeFills(rowShape.Fills, next)
		projection, err := session.NewColumnProjection(old, next, shape.Fills)
		if err != nil {
			return err
		}
		if !rowShape.OwnProjected {
			writes, deletes := scope.ColumnProjectionRoots(address)
			writes, err = projection.Versions(scope.Op.Context(), writes)
			if err != nil {
				return err
			}
			scope.StageProjectedColumnRoots(address, writes, session.ProjectDeletions(deletes, rowShape.Replacement))
		}
		stage.Owner.Mu.Lock()
		snapshot, snapshotExists := scope.Owner.Roots[address]
		stage.Owner.Mu.Unlock()
		if scope.Owner.Isolation.Stable() && snapshotExists && !shape.Rewritten {
			shape.Snapshot, err = session.ProjectVersions(scope.Op.Context(), snapshot.Versions, snapshot.Relation, next, shape.Fills)
			if err != nil {
				return err
			}
			shape.HasSnapshot = true
		}
		changes.Storage[address] = shape
	}
	return nil
}
