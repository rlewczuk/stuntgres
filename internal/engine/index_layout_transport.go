package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
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
	return TransportCapturedIndexLayouts(evaluation, changes, captures, sourceStore, targetStore, sourceWrites, writes)
}

// captureIndexLayouts takes Engine.mu to check the stage's epoch and capture
// the lineages of parent with their committed or private source roots.
func captureIndexLayouts(evaluation *exec.Context, stage *CatalogStage, changes *session.Journal, parent *catalog.Relation, sourceStore, targetStore *storage.Relation) ([]indexLayoutCapture, error) {
	var captures []indexLayoutCapture
	e := stage.owner
	e.mu.Lock()
	if e.catalog.state.Epoch() != stage.Base.Epoch() {
		e.mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	for address, lineage := range stage.indexesOrZero().Layouts {
		if lineage.Source == nil || lineage.Target == nil || lineage.SourceParent == nil || lineage.TargetParent == nil {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		if lineage.TargetParent.Address() != parent.Address() {
			continue
		}
		if lineage.Target.SQLIndex.Parent != parent.Address() || !LayoutInputsUnchanged(lineage.Source, lineage.SourceParent, lineage.TargetParent) || !LayoutInputsUnchanged(lineage.Target, lineage.TargetParent, parent) {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		target, ok := evaluation.Catalog.Relation(address)
		if !ok || !SameIndexKeySemantics(target, lineage.Target) {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		root := e.storage.IndexRoots[address]
		if private := JournalIndexesOrZero(changes).UniqueRoots[address]; private != nil {
			candidate, pinned := JournalIndexesOrZero(changes).Candidates[address]
			if pinned {
				for edge := &lineage; edge != nil; edge = edge.Previous {
					if candidate.MatchesIndex(edge.Source, sourceStore) {
						lineage.Source, lineage.SourceParent = edge.Source, edge.SourceParent
						break
					}
				}
			}
			if !pinned || candidate.Root != private || (!candidate.MatchesIndex(lineage.Source, sourceStore) && !candidate.MatchesIndex(lineage.Target, targetStore)) {
				e.mu.Unlock()
				return nil, ids.ErrCatalogReplaced
			}
			root = private
		} else {
			source, ok := e.catalog.state.Relation(address)
			current := e.storage.Relations[parent.Address()]
			for edge := &lineage; edge != nil; edge = edge.Previous {
				if ok && SameIndexKeySemantics(source, edge.Source) {
					lineage.Source, lineage.SourceParent = edge.Source, edge.SourceParent
					break
				}
			}
			if !ok || !SameIndexKeySemantics(source, lineage.Source) || sourceStore == nil || current != sourceStore.IndexIdentity() || current.Generation != sourceStore.Generation || current.Ends != sourceStore.Ends || len(current.Versions) != len(sourceStore.Versions) {
				e.mu.Unlock()
				return nil, ids.ErrCatalogReplaced
			}
		}
		if root == nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("stuntgres: layout projection is missing its source index root")
		}
		captures = append(captures, indexLayoutCapture{Lineage: lineage, Root: root})
	}
	e.mu.Unlock()
	return captures, nil
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
			changes.Originals[index.Address()] = e.catalog.state.Get(index.Address())
		}
	}
}
