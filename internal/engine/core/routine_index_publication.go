package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

// StageStoredRoutineIndexes is called with Engine.mu held, after the opaque
// scalar publication's descriptors joined this same private stage. The engine,
// not the routine mapper, owns actual committed/private roots and row storage.
func (h DDLHost) StageStoredRoutineIndexes(publication *stored.ScalarRoutinePublication) error {
	e, stage := h.E, h.CatalogStage
	if e == nil || stage == nil || stage.Owner != e || stage.Base.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	target := h.StageRead()
	changes := OperationCatalog(h.Op)
	var journal index.Journal
	if changes != nil {
		journal = index.JournalIndexesOrZero(changes)
	}
	for _, descriptor := range publication.Descriptors() {
		idx, ok := descriptor.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || !idx.SQLIndex.Ready || !idx.SQLIndex.Live {
			continue
		}
		parent, ok := target.Relation(idx.SQLIndex.Parent)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if parent.Partition != nil {
			continue
		}
		previous := journal.Candidates[idx.Address()]
		privateRoot := journal.UniqueRoots[idx.Address()]
		if staged, exists := stage.IndexesOrZero().Candidates[idx.Address()]; exists {
			previous, privateRoot = staged, staged.Root
		}
		input := index.RoutineRootInput{
			Publication: publication, Target: target, Index: idx, Committed: e.Catalog.Current(), CommittedRoots: e.Storage.IndexRoots, CommittedStores: e.Storage.Relations,
			Store: e.PrivateStorageLocked(changes, parent.Address()), Previous: previous, PrivateRoot: privateRoot, Rebuilds: stage.IndexesOrZero().Rebuilds,
		}
		if changes != nil {
			input.OriginalIndex, input.OriginalParent = changes.Originals[idx.Address()], changes.Originals[parent.Address()]
		}
		candidate, err := index.PreserveRoutineRoot(input)
		if err != nil {
			return err
		}
		stage.recordIndexCandidate(idx.Address(), candidate)
	}
	return nil
}
