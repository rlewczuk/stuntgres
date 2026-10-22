package index

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// LayoutCaptureInput is what CaptureLayouts reads. The caller fills it under
// Engine.mu after checking the stage's epoch, and holds the lock until
// CaptureLayouts returns.
type LayoutCaptureInput struct {
	Layouts map[ids.ObjectAddress]LayoutLineage // The stage's layout lineages.
	Target  catalog.Read                        // The catalog the evaluation reads.
	Parent  *catalog.Relation
	// Committed, CommittedRoots and CommittedStore are the published catalog,
	// index roots and the parent's published row storage.
	Committed      *catalog.State
	CommittedRoots map[ids.ObjectAddress]*storage.IndexRoot
	CommittedStore *storage.Relation
	Changes        *session.Journal
	SourceStore    *storage.Relation
	TargetStore    *storage.Relation
}

// CaptureLayouts captures the lineages of in.Parent with their committed or
// private source roots. A lineage whose inputs changed is ErrCatalogReplaced.
func CaptureLayouts(in LayoutCaptureInput) ([]LayoutCapture, error) {
	var captures []LayoutCapture
	parent, changes, sourceStore, targetStore := in.Parent, in.Changes, in.SourceStore, in.TargetStore
	for address, lineage := range in.Layouts {
		if lineage.Source == nil || lineage.Target == nil || lineage.SourceParent == nil || lineage.TargetParent == nil {
			return nil, ids.ErrCatalogReplaced
		}
		if lineage.TargetParent.Address() != parent.Address() {
			continue
		}
		if lineage.Target.SQLIndex.Parent != parent.Address() || !LayoutInputsUnchanged(lineage.Source, lineage.SourceParent, lineage.TargetParent) || !LayoutInputsUnchanged(lineage.Target, lineage.TargetParent, parent) {
			return nil, ids.ErrCatalogReplaced
		}
		target, ok := in.Target.Relation(address)
		if !ok || !SameIndexKeySemantics(target, lineage.Target) {
			return nil, ids.ErrCatalogReplaced
		}
		root := in.CommittedRoots[address]
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
				return nil, ids.ErrCatalogReplaced
			}
			root = private
		} else {
			source, ok := in.Committed.Relation(address)
			current := in.CommittedStore
			for edge := &lineage; edge != nil; edge = edge.Previous {
				if ok && SameIndexKeySemantics(source, edge.Source) {
					lineage.Source, lineage.SourceParent = edge.Source, edge.SourceParent
					break
				}
			}
			if !ok || !SameIndexKeySemantics(source, lineage.Source) || sourceStore == nil || current != sourceStore.IndexIdentity() || current.Generation != sourceStore.Generation || current.Ends != sourceStore.Ends || len(current.Versions) != len(sourceStore.Versions) {
				return nil, ids.ErrCatalogReplaced
			}
		}
		if root == nil {
			return nil, fmt.Errorf("stuntgres: layout projection is missing its source index root")
		}
		captures = append(captures, LayoutCapture{Lineage: lineage, Root: root})
	}
	return captures, nil
}
