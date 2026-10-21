package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
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
func (e *Engine) prepareCatalogOverlayIndexes(scope *StatementScope, stage *CatalogStage, c *session.Journal) error {
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
	stageIndexes := stage.indexesOrZero()
	for a, d := range view.Descriptors() {
		idx, ok := d.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			continue
		}
		parent, ok := view.Relation(idx.SQLIndex.Parent)
		if !ok {
			e.mu.Unlock()
			return catalog.Conflict{}
		}
		old, existed := stage.Base.Relation(parent.Address())
		shape, replaced := c.Storage[parent.Address()]
		storageChanged := replaced && shape.Replacement != e.privateStorageLocked(scope.owner.Catalog, parent.Address())
		if !storageChanged && stage.Get(a) == nil && stageIndexes.Rebuilds[a] == nil && (!existed || slices.Equal(old.Attributes, parent.Attributes)) {
			continue
		}
		if !idx.SQLIndex.Ready {
			index.JournalOf(c.Indexes).Forget(a)
			continue
		}
		// Virtual indexes own ancestry/validity metadata, never heap captures or
		// installation roots. Their exact descriptor fences were captured above.
		if parent.Partition != nil {
			continue
		}
		// Unchanged key semantics permit root reuse only on the same storage
		// incarnation; a replacement heap needs its own physical baseline.
		if previous, ok := stage.Base.Relation(a); !storageChanged && ok && previous.SQLIndex != nil && previous.SQLIndex.Ready && stageIndexes.Rebuilds[a] == nil && existed && slices.Equal(old.Attributes, parent.Attributes) && index.SameIndexKeySemantics(previous, idx) {
			continue
		}
		store := e.privateStorageLocked(c, parent.Address())
		if store == nil {
			e.mu.Unlock()
			return catalog.Conflict{}
		}
		if _, produced := stageIndexes.Candidates[a]; produced {
			candidate, exists := index.JournalIndexesOrZero(c).Candidates[a]
			if !exists || !candidate.MatchesIndex(idx, store) {
				e.mu.Unlock()
				return catalog.Conflict{}
			}
			continue
		}
		if _, transported := stageIndexes.Layouts[a]; transported {
			if candidate, exists := index.JournalIndexesOrZero(c).Candidates[a]; exists && candidate.MatchesIndex(idx, store) && stageIndexes.Rebuilds[a] == nil {
				continue
			}
		}
		snapshot := *store
		snapshot.IndexSource = store.IndexIdentity()
		captures = append(captures, catalogOverlayIndex{index: idx, parent: parent, store: snapshot})
		committed := e.storage.Relations[parent.Address()]
		fence := storageReadFence{address: parent.Address(), storage: committed}
		if committed != nil {
			fence.length = len(committed.Versions)
			fence.ends = committed.Ends
			fence.generation = committed.Generation
		}
		c.AddFence(fence)
	}
	e.mu.Unlock()
	for i := range captures {
		capture := &captures[i]
		idx, parent := capture.index, capture.parent
		building := true
		indexAuth := auth
		if r := stage.materializedOrZero().Replacement; r != nil && r.Concurrent && r.Original.Address() == parent.Address() {
			building = false
			indexAuth.State.CurrentUser = parent.Owner
			if _, err := indexAuth.Roles.Descriptor(parent.Owner); err != nil {
				return err
			}
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		evaluation.RoutineAuthority = &auth
		if idx.SQLIndex.HasRuntimeDomainConversion() {
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
		visible, err := index.BuildVisibleStore(scope.op.Context(), &capture.store, writes, deletes)
		if err != nil {
			return err
		}
		var outputs *index.BuildCapture
		if idx.SQLIndex.HasRuntimeDomainConversion() || len(writes) != 0 {
			outputs = &index.BuildCapture{Index: idx}
		}
		if outputs != nil {
			outputs.Outputs = make([]storage.IndexOutput, len(visible.Versions))
		}
		full, err := index.BuildIndexRoot(&evaluation, parent, idx.SQLIndex, visible, outputs)
		if err != nil {
			return err
		}
		schema := view.Get(parent.Namespace()).Name()
		if err := index.ValidateUniqueRoot(scope.op.Context(), parent, idx, full, schema, scope.output, &indexAuth.View, disclosureEnv(&indexAuth), building); err != nil {
			return err
		}
		build := &index.AlterIndexBuild{Index: idx, Root: full, Store: visible}
		if outputs != nil {
			build.Outputs = outputs.Outputs
		}
		var positions map[index.RowIdentity]int
		if visible != &capture.store {
			positions, err = index.BuildPositions(scope.op.Context(), &capture.store)
			if err != nil {
				return err
			}
		}
		baseline, err := build.BaselineRoot(scope.op.Context(), idx, &capture.store, positions)
		if err != nil {
			return err
		}
		index.RetainIndexBuild(c, idx, &capture.store, baseline)
		updated, changed, err := index.AttachIndexBuildOutputs(scope.op.Context(), writes, map[string]*index.AlterIndexBuild{idx.Name(): build})
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
