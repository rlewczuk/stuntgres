package engine

import (
	"slices"

	"stuntgres/internal/engine/ids"
)

type catalogOverlayIndex struct {
	index, parent *relationDescriptor
	store         relationStorage
}

// Ordinary key providers run only on detached storage captures, never under
// publication locks. The committed baseline remains distinct from own rows;
// reconstructing the overlay validates CREATE/REINDEX at this statement, with
// build diagnostics, rather than incorrectly delaying errors until COMMIT.
func (e *Engine) prepareCatalogOverlayIndexes(scope *StatementScope, stage *CatalogStage, c *catalogChanges) error {
	e.mu.Lock()
	if scope.txRef.Epoch() != e.catalog.state.epoch {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(c)
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	auth.catalog = view
	auth.recaptureCatalog(auth.catalog)
	var captures []catalogOverlayIndex
	for a, d := range view.Descriptors() {
		index, ok := d.(*relationDescriptor)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent, ok := view.Relation(index.SQLIndex.parent)
		if !ok {
			e.mu.Unlock()
			return CatalogConflict{}
		}
		old, existed := stage.base.Relation(parent.address)
		shape, replaced := c.storage[parent.address]
		storageChanged := replaced && shape.replacement != e.privateStorageLocked(scope.owner.catalog, parent.address)
		if !storageChanged && stage.descriptors[a] == nil && stage.indexRebuilds[a] == nil && (!existed || slices.Equal(old.Attributes, parent.Attributes)) {
			continue
		}
		if !index.SQLIndex.ready {
			delete(c.uniqueRoots, a)
			delete(c.indexCandidates, a)
			continue
		}
		// Virtual indexes own ancestry/validity metadata, never heap captures or
		// installation roots. Their exact descriptor fences were captured above.
		if parent.Partition != nil {
			continue
		}
		// Unchanged key semantics permit root reuse only on the same storage
		// incarnation; a replacement heap needs its own physical baseline.
		if previous, ok := stage.base.Relation(a); !storageChanged && ok && previous.SQLIndex != nil && previous.SQLIndex.ready && stage.indexRebuilds[a] == nil && existed && slices.Equal(old.Attributes, parent.Attributes) && sameIndexKeySemantics(previous, index) {
			continue
		}
		store := e.privateStorageLocked(c, parent.address)
		if store == nil {
			e.mu.Unlock()
			return CatalogConflict{}
		}
		if _, produced := stage.indexCandidates[a]; produced {
			candidate, exists := c.indexCandidates[a]
			if !exists || !candidate.matchesIndex(index, store) {
				e.mu.Unlock()
				return CatalogConflict{}
			}
			continue
		}
		if _, transported := stage.indexLayouts[a]; transported {
			if candidate, exists := c.indexCandidates[a]; exists && candidate.matchesIndex(index, store) && stage.indexRebuilds[a] == nil {
				continue
			}
		}
		snapshot := *store
		snapshot.indexSource = indexStorageIdentity(store)
		captures = append(captures, catalogOverlayIndex{index: index, parent: parent, store: snapshot})
		committed := e.storage[parent.address]
		fence := partitionReadFence{address: parent.address, storage: committed}
		if committed != nil {
			fence.length = len(committed.versions)
			fence.ends = committed.ends
			fence.generation = committed.generation
		}
		c.readFences = append(c.readFences, fence)
	}
	e.mu.Unlock()
	for i := range captures {
		capture := &captures[i]
		index, parent := capture.index, capture.parent
		building := true
		indexAuth := auth
		if r := stage.materializedReplacement; r != nil && r.concurrent && r.original.address == parent.address {
			building = false
			indexAuth.state.CurrentUser = parent.Owner
			if _, err := indexAuth.roles.Descriptor(parent.Owner); err != nil {
				return err
			}
		}
		evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: view}
		evaluation.routineAuthority = &auth
		if index.SQLIndex.hasRuntimeDomainConversion() {
			var err error
			evaluation.Input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(indexAuth)
			if err != nil {
				return err
			}
		}
		writes, deletes := scope.columnProjectionRoots(parent.address)
		if stage.initialTables[parent.address] != nil {
			writes = scope.stage.rows[parent.address]
		}
		visible, err := indexBuildVisibleStore(scope.op.Context(), &capture.store, writes, deletes)
		if err != nil {
			return err
		}
		var outputs *indexBuildCapture
		if index.SQLIndex.hasRuntimeDomainConversion() || len(writes) != 0 {
			outputs = &indexBuildCapture{index: index}
		}
		if outputs != nil {
			outputs.outputs = make([]indexRowOutput, len(visible.versions))
		}
		full, err := buildIndexRoot(&evaluation, parent, index.SQLIndex, visible, outputs)
		if err != nil {
			return err
		}
		schema := view.Get(parent.namespace).Name()
		if err := validateUniqueRoot(scope.op.Context(), parent, index, full, schema, scope.output, &indexAuth, building); err != nil {
			return err
		}
		build := &alterIndexBuild{index: index, root: full, store: visible}
		if outputs != nil {
			build.outputs = outputs.outputs
		}
		var positions map[indexRowIdentity]int
		if visible != &capture.store {
			positions, err = indexBuildPositions(scope.op.Context(), &capture.store)
			if err != nil {
				return err
			}
		}
		baseline, err := build.baselineRoot(scope.op.Context(), index, &capture.store, positions)
		if err != nil {
			return err
		}
		retainIndexBuild(c, index, &capture.store, baseline)
		updated, changed, err := attachIndexBuildOutputs(scope.op.Context(), writes, map[string]*alterIndexBuild{index.name: build})
		if err != nil {
			return err
		}
		if changed {
			if stage.initialTables[parent.address] != nil {
				// These rows were produced by this CREATE statement, not an
				// earlier transaction prefix owned by the projection helper.
				scope.stage.rows[parent.address] = updated
			} else {
				scope.stageProjectedColumnRoots(parent.address, updated, deletes)
			}
		}
	}
	return scope.op.Context().Err()
}
