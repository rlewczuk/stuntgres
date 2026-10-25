package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Engine.mu is held. Reuse the native snapshot scalar/key transport, retaining
// every version and every produced predicate/expression result. ALTER must not
// turn a metadata generation change into another CHECK or index truth event.
func (e *Engine) stageDomainIndexGenerationsLocked(stage *CatalogStage, next *catalog.State, stores map[ids.ObjectAddress]*storage.Relation) error {
	for address, store := range stores {
		rebound, err := index.ReownDomainIndexOutputs(stage.domainAlter.AlterContext(), stage.Base.State(), next, e.StageStorageLocked(stage, address), store)
		if err != nil {
			return err
		}
		stores[address] = rebound
	}
	sources := make(map[ids.ObjectAddress]*storage.Relation, len(stores))
	for address := range stores {
		sources[address] = e.StageStorageLocked(stage, address)
	}
	sourceRoots := make(map[ids.ObjectAddress]*storage.IndexRoot)
	for address, descriptor := range stage.Base.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && sources[index.SQLIndex.Parent] != nil {
			sourceRoots[address] = e.domainStageIndexRootLocked(stage, address)
		}
	}
	var private *session.Journal
	if stage.AuthSession != nil && (stage.AuthSession.Transactions.State == session.TxActive || stage.AuthSession.Transactions.RetainsTransaction()) {
		private = stage.AuthSession.Transactions.Catalog
	}
	snapshot, err := e.captureIndexSnapshotParents(stage.domainAlter.AlterContext(), stage.Base, sources, sourceRoots, e.Visibility.CommitSeq(), stores, private)
	if err != nil {
		return err
	}
	if len(snapshot.Sources) == 0 {
		return nil
	}
	positions := make(map[ids.ObjectAddress]map[index.RowIdentity]int)
	for _, source := range snapshot.Sources {
		address := source.Parent.Address()
		if positions[address] != nil {
			continue
		}
		store := stores[address]
		selected := make(map[index.RowIdentity]int, len(store.Versions))
		for position, row := range store.Versions {
			selected[index.RowIdentity{Row: row.Row, Version: row.Version}] = position
		}
		positions[address] = selected
	}
	roots, err := snapshot.TransportRows(stage.domainAlter.AlterContext(), stage.Base.WithState(next), stores, positions, true)
	if err != nil {
		return err
	}
	if stage.IndexesOrZero().Candidates == nil {
		stage.Indexes().Candidates = make(map[ids.ObjectAddress]index.RootCandidate, len(roots))
	}
	domain := stage.DomainAlterState()
	if domain.IndexRoots == nil {
		domain.IndexRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(roots))
	}
	for address, root := range roots {
		idx := next.MustRelation(address)
		store := stores[idx.SQLIndex.Parent]
		stage.Indexes().Candidates[address] = index.RootCandidate{Index: idx, Root: root, Storage: store.IndexIdentity(), Generation: store.Generation, Ends: store.Ends, Length: len(store.Versions)}
		domain.IndexRoots[address] = snapshot.Sources[address].Root
	}
	return nil
}
