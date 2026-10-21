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
	if len(stage.indexesOrZero().layouts) == 0 {
		return writes, false, nil
	}
	captures, err := captureIndexLayouts(evaluation, stage, changes, parent, sourceStore, targetStore)
	if err != nil {
		return nil, false, err
	}
	return transportCapturedIndexLayouts(evaluation, changes, captures, sourceStore, targetStore, sourceWrites, writes)
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
	for address, lineage := range stage.indexesOrZero().layouts {
		if lineage.source == nil || lineage.target == nil || lineage.sourceParent == nil || lineage.targetParent == nil {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		if lineage.targetParent.Address() != parent.Address() {
			continue
		}
		if lineage.target.SQLIndex.Parent != parent.Address() || !indexLayoutInputsUnchanged(lineage.source, lineage.sourceParent, lineage.targetParent) || !indexLayoutInputsUnchanged(lineage.target, lineage.targetParent, parent) {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		target, ok := evaluation.Catalog.Relation(address)
		if !ok || !sameIndexKeySemantics(target, lineage.target) {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		root := e.storage.IndexRoots[address]
		if private := journalIndexesOrZero(changes).uniqueRoots[address]; private != nil {
			candidate, pinned := journalIndexesOrZero(changes).candidates[address]
			if pinned {
				for edge := &lineage; edge != nil; edge = edge.previous {
					if candidate.matchesIndex(edge.source, sourceStore) {
						lineage.source, lineage.sourceParent = edge.source, edge.sourceParent
						break
					}
				}
			}
			if !pinned || candidate.root != private || (!candidate.matchesIndex(lineage.source, sourceStore) && !candidate.matchesIndex(lineage.target, targetStore)) {
				e.mu.Unlock()
				return nil, ids.ErrCatalogReplaced
			}
			root = private
		} else {
			source, ok := e.catalog.state.Relation(address)
			current := e.storage.Relations[parent.Address()]
			for edge := &lineage; edge != nil; edge = edge.previous {
				if ok && sameIndexKeySemantics(source, edge.source) {
					lineage.source, lineage.sourceParent = edge.source, edge.sourceParent
					break
				}
			}
			if !ok || !sameIndexKeySemantics(source, lineage.source) || sourceStore == nil || current != sourceStore.IndexIdentity() || current.Generation != sourceStore.Generation || current.Ends != sourceStore.Ends || len(current.Versions) != len(sourceStore.Versions) {
				e.mu.Unlock()
				return nil, ids.ErrCatalogReplaced
			}
		}
		if root == nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("stuntgres: layout projection is missing its source index root")
		}
		captures = append(captures, indexLayoutCapture{lineage: lineage, root: root})
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
