package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Engine.mu is held. Reuse the native snapshot scalar/key transport, retaining
// every version and every produced predicate/expression result. ALTER must not
// turn a metadata generation change into another CHECK or index truth event.
func (e *Engine) stageDomainIndexGenerationsLocked(stage *CatalogStage, next *catalog.State, stores map[ids.ObjectAddress]*relationStorage) error {
	for address, store := range stores {
		rebound, err := reownDomainIndexOutputs(stage.domainAlter, stage.Base.State(), next, e.stageStorageLocked(stage, address), store)
		if err != nil {
			return err
		}
		stores[address] = rebound
	}
	sources := make(map[ids.ObjectAddress]*relationStorage, len(stores))
	for address := range stores {
		sources[address] = e.stageStorageLocked(stage, address)
	}
	sourceRoots := make(map[ids.ObjectAddress]*storage.IndexRoot)
	for address, descriptor := range stage.Base.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && sources[index.SQLIndex.Parent] != nil {
			sourceRoots[address] = e.domainStageIndexRootLocked(stage, address)
		}
	}
	var private *catalogChanges
	if stage.authSession != nil && stage.authSession.transactions.retainsTransaction() {
		private = stage.authSession.transactions.catalog
	}
	snapshot, err := e.captureIndexSnapshotParents(stage.domainAlter, stage.Base, sources, sourceRoots, e.commitSeq, stores, private)
	if err != nil {
		return err
	}
	if len(snapshot.sources) == 0 {
		return nil
	}
	positions := make(map[ids.ObjectAddress]map[indexRowIdentity]int)
	for _, source := range snapshot.sources {
		address := source.parent.Address()
		if positions[address] != nil {
			continue
		}
		store := stores[address]
		selected := make(map[indexRowIdentity]int, len(store.Versions))
		for position, row := range store.Versions {
			selected[indexRowIdentity{row: row.Row, version: row.Version}] = position
		}
		positions[address] = selected
	}
	roots, err := snapshot.transportRows(stage.domainAlter, next, stores, positions, true)
	if err != nil {
		return err
	}
	if stage.indexCandidates == nil {
		stage.indexCandidates = make(map[ids.ObjectAddress]indexRootCandidate, len(roots))
	}
	if stage.domainIndexRoots == nil {
		stage.domainIndexRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(roots))
	}
	for address, root := range roots {
		index := next.MustRelation(address)
		store := stores[index.SQLIndex.Parent]
		stage.indexCandidates[address] = indexRootCandidate{index: index, root: root, storage: store.IndexIdentity(), generation: store.Generation, ends: store.Ends, length: len(store.Versions)}
		stage.domainIndexRoots[address] = snapshot.sources[address].root
	}
	return nil
}
