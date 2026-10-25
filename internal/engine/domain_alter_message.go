package engine

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
)

// Stage observations describe the exact private entry roots. Outer publication
// instead fences their committed predecessors, never private-vs-committed pointer
// equality and never another execution of the selected CHECK.
func (e *Engine) captureDomainStageLocked(stage *CatalogStage, changes *session.Journal, current *catalog.State) error {
	if fence := stage.domainAlter.ValidationFence(); fence != nil {
		stores := make(map[ids.ObjectAddress]*storage.Relation, len(fence.Uses))
		for _, use := range fence.Uses {
			stores[use.Relation.Address()] = e.StageStorageLocked(stage, use.Relation.Address())
		}
		if !fence.Current(e.Catalog.Read(current), stores) {
			return catalog.Conflict{}
		}
		committed, err := typedef.DomainUsesAt(e.Catalog.Current(), fence.Target, fence.Path)
		if committed == nil {
			return err
		}
		// A prior own DROP may hide a committed stored container. Its presence
		// is an observed predecessor edge, not another validation blocker.
		committed.AllowContainers = true
		for i := range committed.Uses {
			use := &committed.Uses[i]
			use.Storage = e.Storage.Relations[use.Relation.Address()]
			if use.Storage == nil {
				return catalog.Conflict{}
			}
			use.Versions = use.Storage.Versions[:len(use.Storage.Versions):len(use.Storage.Versions)]
			use.Ends, use.Generation = use.Storage.Ends, use.Storage.Generation
		}
		journal := typedef.DomainWritable(changes)
		journal.Validations = append(journal.Validations, committed)
	}
	if len(stage.domainAlter.AlteredIndexRoots()) != 0 {
		journal := typedef.DomainWritable(changes)
		journal.IndexRoots = maps.Clone(journal.IndexRoots)
		if journal.IndexRoots == nil {
			journal.IndexRoots = make(map[ids.ObjectAddress]*storage.IndexRoot)
		}
		for address, root := range stage.domainAlter.AlteredIndexRoots() {
			if e.domainStageIndexRootLocked(stage, address) != root {
				return catalog.Conflict{}
			}
			journal.IndexRoots[address] = e.Storage.IndexRoots[address]
		}
	}
	return nil
}

func (e *Engine) domainStageIndexRootLocked(stage *CatalogStage, address ids.ObjectAddress) *storage.IndexRoot {
	var changes *session.Journal
	if stage.AuthSession != nil && (stage.AuthSession.Transactions.State == session.TxActive || stage.AuthSession.Transactions.RetainsTransaction()) {
		changes = stage.AuthSession.Transactions.Catalog
	}
	return index.EffectiveRoot(e.Storage.IndexRoots, changes, address)
}

func (scope *StatementScope) reownTypeOverlayRows(stage *CatalogStage, changes *session.Journal) error {
	e := scope.Engine()
	e.Mu.Lock()
	next := e.PrivateCatalogLocked(changes).State()
	e.Mu.Unlock()
	for address, rowShape := range stage.RowShapes {
		writes, deletes := scope.ColumnProjectionRoots(address)
		mapped, err := stored.ReownVersions(next, writes)
		if err != nil {
			return err
		}
		original, target := storage.Relation{Versions: writes}, storage.Relation{Versions: mapped}
		rebound, err := index.ReownDomainIndexOutputs(scope.Op.Context(), stage.Base.State(), next, &original, &target)
		if err != nil {
			return err
		}
		scope.StageProjectedColumnRoots(address, rebound.Versions, session.ProjectDeletions(deletes, rowShape.Replacement))
		shape := changes.Storage[address]
		if len(shape.Fills) != 0 {
			shape.Fills = maps.Clone(shape.Fills)
			for attribute, value := range shape.Fills {
				target := next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(value.Type().ID())})
				shape.Fills[attribute], err = typedef.ReownDomainDatum(value, target.SQLType, nil, next.ScalarImports(), next.ACLItemImport())
				if err != nil {
					return err
				}
			}
		}
		if scope.Owner.Isolation.Stable() && !shape.HasSnapshot && !shape.Rewritten {
			scope.Owner.Mu.Lock()
			snapshot, exists := scope.Owner.Roots[address]
			scope.Owner.Mu.Unlock()
			if exists {
				shape.Snapshot, shape.HasSnapshot = snapshot.Versions, true
			}
		}
		if shape.HasSnapshot {
			shape.Snapshot, err = stored.ReownVersions(next, shape.Snapshot)
			if err != nil {
				return err
			}
		}
		changes.Storage[address] = shape
	}
	return nil
}
