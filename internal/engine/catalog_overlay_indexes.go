package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

type catalogOverlayIndex struct {
	index, parent *catalog.Relation
	store         storage.Relation
}

// Ordinary key providers run only on detached storage captures, never under
// publication locks. The committed baseline remains distinct from own rows;
// reconstructing the overlay validates CREATE/REINDEX at this statement, with
// build diagnostics, rather than incorrectly delaying errors until COMMIT.
func (e *Engine) prepareCatalogOverlayIndexes(scope *StatementScope, stage *CatalogStage, c *catalogChanges) error {
	e.mu.Lock()
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(c)
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	var captures []catalogOverlayIndex
	for a, d := range view.Descriptors() {
		index, ok := d.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent, ok := view.Relation(index.SQLIndex.Parent)
		if !ok {
			e.mu.Unlock()
			return catalog.Conflict{}
		}
		old, existed := stage.Base.Relation(parent.Address())
		shape, replaced := c.storage[parent.Address()]
		storageChanged := replaced && shape.replacement != e.privateStorageLocked(scope.owner.catalog, parent.Address())
		if !storageChanged && stage.Descriptors[a] == nil && stage.indexRebuilds[a] == nil && (!existed || slices.Equal(old.Attributes, parent.Attributes)) {
			continue
		}
		if !index.SQLIndex.Ready {
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
		if previous, ok := stage.Base.Relation(a); !storageChanged && ok && previous.SQLIndex != nil && previous.SQLIndex.Ready && stage.indexRebuilds[a] == nil && existed && slices.Equal(old.Attributes, parent.Attributes) && sameIndexKeySemantics(previous, index) {
			continue
		}
		store := e.privateStorageLocked(c, parent.Address())
		if store == nil {
			e.mu.Unlock()
			return catalog.Conflict{}
		}
		if _, produced := stage.indexCandidates[a]; produced {
			candidate, exists := c.indexCandidates[a]
			if !exists || !candidate.matchesIndex(index, store) {
				e.mu.Unlock()
				return catalog.Conflict{}
			}
			continue
		}
		if _, transported := stage.indexLayouts[a]; transported {
			if candidate, exists := c.indexCandidates[a]; exists && candidate.matchesIndex(index, store) && stage.indexRebuilds[a] == nil {
				continue
			}
		}
		snapshot := *store
		snapshot.IndexSource = store.IndexIdentity()
		captures = append(captures, catalogOverlayIndex{index: index, parent: parent, store: snapshot})
		committed := e.storage.Relations[parent.Address()]
		fence := partitionReadFence{address: parent.Address(), storage: committed}
		if committed != nil {
			fence.length = len(committed.Versions)
			fence.ends = committed.Ends
			fence.generation = committed.Generation
		}
		c.readFences = append(c.readFences, fence)
	}
	e.mu.Unlock()
	for i := range captures {
		capture := &captures[i]
		index, parent := capture.index, capture.parent
		building := true
		indexAuth := auth
		if r := stage.materializedReplacement; r != nil && r.concurrent && r.original.Address() == parent.Address() {
			building = false
			indexAuth.State.CurrentUser = parent.Owner
			if _, err := indexAuth.Roles.Descriptor(parent.Owner); err != nil {
				return err
			}
		}
		evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: view}
		evaluation.routineAuthority = &auth
		if index.SQLIndex.HasRuntimeDomainConversion() {
			var err error
			evaluation.Input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(indexAuth)
			if err != nil {
				return err
			}
		}
		writes, deletes := scope.columnProjectionRoots(parent.Address())
		if stage.initialTables[parent.Address()] != nil {
			writes = scope.stage.rows[parent.Address()]
		}
		visible, err := indexBuildVisibleStore(scope.op.Context(), &capture.store, writes, deletes)
		if err != nil {
			return err
		}
		var outputs *indexBuildCapture
		if index.SQLIndex.HasRuntimeDomainConversion() || len(writes) != 0 {
			outputs = &indexBuildCapture{index: index}
		}
		if outputs != nil {
			outputs.outputs = make([]storage.IndexOutput, len(visible.Versions))
		}
		full, err := buildIndexRoot(&evaluation, parent, index.SQLIndex, visible, outputs)
		if err != nil {
			return err
		}
		schema := view.Get(parent.Namespace()).Name()
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
		updated, changed, err := attachIndexBuildOutputs(scope.op.Context(), writes, map[string]*alterIndexBuild{index.Name(): build})
		if err != nil {
			return err
		}
		if changed {
			if stage.initialTables[parent.Address()] != nil {
				// These rows were produced by this CREATE statement, not an
				// earlier transaction prefix owned by the projection helper.
				scope.stage.rows[parent.Address()] = updated
			} else {
				scope.stageProjectedColumnRoots(parent.Address(), updated, deletes)
			}
		}
	}
	return scope.op.Context().Err()
}
