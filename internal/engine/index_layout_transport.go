package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// A layout projection preserves surviving cells and exact row/version order.
// Both baseline entries and private row outputs therefore travel without SQL.
func transportIndexLayouts(evaluation *exec.Context, stage *CatalogStage, changes *session.Journal, parent *catalog.Relation, sourceStore, targetStore *storage.Relation, sourceWrites, writes []storage.RowVersion) ([]storage.RowVersion, bool, error) {
	if len(stage.indexesOrZero().Layouts) == 0 {
		return writes, false, nil
	}
	captures, err := captureIndexLayouts(evaluation, stage, changes, parent, sourceStore, targetStore)
	if err != nil {
		return nil, false, err
	}
	return index.TransportCapturedIndexLayouts(evaluation, changes, captures, sourceStore, targetStore, sourceWrites, writes)
}

// captureIndexLayouts takes Engine.mu to check the stage's epoch and capture
// the lineages of parent with their committed or private source roots.
func captureIndexLayouts(evaluation *exec.Context, stage *CatalogStage, changes *session.Journal, parent *catalog.Relation, sourceStore, targetStore *storage.Relation) ([]index.LayoutCapture, error) {
	e := stage.owner
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.catalog.State.Epoch() != stage.Base.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	return index.CaptureLayouts(index.LayoutCaptureInput{
		Layouts: stage.indexesOrZero().Layouts, Target: evaluation.Catalog, Parent: parent,
		Committed: e.catalog.State, CommittedRoots: e.storage.IndexRoots, CommittedStore: e.storage.Relations[parent.Address()],
		Changes: changes, SourceStore: sourceStore, TargetStore: targetStore,
	})
}

// Preserve exact committed descriptor predecessors while empty roots are built
// unlocked; the normal catalog preflight owns the publication veto.
func (e *Engine) fenceEmptyIndexStorageLocked(snapshot *catalog.State, changes *session.Journal, address ids.ObjectAddress) {
	for _, descriptor := range snapshot.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != address {
			continue
		}
		if _, captured := changes.Originals[index.Address()]; !captured {
			changes.Originals[index.Address()] = e.catalog.State.Get(index.Address())
		}
	}
}
