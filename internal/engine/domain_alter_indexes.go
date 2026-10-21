package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Engine.mu is held. Reuse the native snapshot scalar/key transport, retaining
// every version and every produced predicate/expression result. ALTER must not
// turn a metadata generation change into another CHECK or index truth event.
func (e *Engine) stageDomainIndexGenerationsLocked(stage *CatalogStage, next *catalog.State, stores map[ids.ObjectAddress]*storage.Relation) error {
	for address, store := range stores {
		rebound, err := ReownDomainIndexOutputs(stage.domainAlter.AlterContext(), stage.Base.State(), next, e.stageStorageLocked(stage, address), store)
		if err != nil {
			return err
		}
		stores[address] = rebound
	}
	sources := make(map[ids.ObjectAddress]*storage.Relation, len(stores))
	for address := range stores {
		sources[address] = e.stageStorageLocked(stage, address)
	}
	sourceRoots := make(map[ids.ObjectAddress]*storage.IndexRoot)
	for address, descriptor := range stage.Base.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && sources[index.SQLIndex.Parent] != nil {
			sourceRoots[address] = e.domainStageIndexRootLocked(stage, address)
		}
	}
	var private *session.Journal
	if stage.authSession != nil && stage.authSession.Transactions.RetainsTransaction() {
		private = stage.authSession.Transactions.Catalog
	}
	snapshot, err := e.captureIndexSnapshotParents(stage.domainAlter.AlterContext(), stage.Base, sources, sourceRoots, e.visibility.CommitSeq(), stores, private)
	if err != nil {
		return err
	}
	if len(snapshot.Sources) == 0 {
		return nil
	}
	positions := make(map[ids.ObjectAddress]map[indexRowIdentity]int)
	for _, source := range snapshot.Sources {
		address := source.Parent.Address()
		if positions[address] != nil {
			continue
		}
		store := stores[address]
		selected := make(map[indexRowIdentity]int, len(store.Versions))
		for position, row := range store.Versions {
			selected[indexRowIdentity{Row: row.Row, Version: row.Version}] = position
		}
		positions[address] = selected
	}
	roots, err := snapshot.TransportRows(stage.domainAlter.AlterContext(), next, stores, positions, true)
	if err != nil {
		return err
	}
	if stage.indexesOrZero().Candidates == nil {
		stage.indexes().Candidates = make(map[ids.ObjectAddress]indexRootCandidate, len(roots))
	}
	domain := stage.domainAlterState()
	if domain.IndexRoots == nil {
		domain.IndexRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(roots))
	}
	for address, root := range roots {
		index := next.MustRelation(address)
		store := stores[index.SQLIndex.Parent]
		stage.indexes().Candidates[address] = indexRootCandidate{Index: index, Root: root, Storage: store.IndexIdentity(), Generation: store.Generation, Ends: store.Ends, Length: len(store.Versions)}
		domain.IndexRoots[address] = snapshot.Sources[address].Root
	}
	return nil
}
