package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	sourceRoots := make(map[ids.ObjectAddress]*logicalIndexRoot)
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
		selected := make(map[indexRowIdentity]int, len(store.versions))
		for position, row := range store.versions {
			selected[indexRowIdentity{row: row.row, version: row.version}] = position
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
		stage.domainIndexRoots = make(map[ids.ObjectAddress]*logicalIndexRoot, len(roots))
	}
	for address, root := range roots {
		index := next.MustRelation(address)
		store := stores[index.SQLIndex.Parent]
		stage.indexCandidates[address] = indexRootCandidate{index: index, root: root, storage: indexStorageIdentity(store), generation: store.generation, ends: store.ends, length: len(store.versions)}
		stage.domainIndexRoots[address] = snapshot.sources[address].root
	}
	return nil
}

func (e *Engine) domainIndexRootsCurrent(stage *CatalogStage) bool {
	for address, root := range stage.domainIndexRoots {
		if e.indexRoots[address] != root {
			return false
		}
	}
	return true
}
