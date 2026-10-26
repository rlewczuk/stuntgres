package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Convert an already validated ordinary catalog stage into the transaction's
// immutable overlay. New stores use committed absence as their honest fence.
func (e *Engine) StageCatalogOverlay(scope *StatementScope, stage *CatalogStage, changes *session.Journal) error {
	e.Mu.Lock()
	if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if stage.InitialTx != (ids.TxRef{}) && stage.InitialTx != scope.TxRef {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	prior := scope.Catalog
	if prior == nil {
		prior = scope.Owner.Catalog
	}
	err := e.captureCatalogStageLocked(stage, changes, e.PrivateCatalogLocked(prior).State(), scope.CatalogAuthority)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if stage.RemovalCount() > 0 {
		scope.DiscardRemovedWrites(stage)
	}
	if err = scope.projectCatalogOverlayShapes(stage, changes); err != nil {
		return err
	}
	if err = scope.reownTypeOverlayRows(stage, changes); err != nil {
		return err
	}
	if r := stage.MaterializedOrZero().Replacement; r != nil {
		// A private CREATE WITH DATA has own rows on the old empty store.
		// REFRESH replaces that complete target, not an append to those rows.
		a := r.Original.Address()
		if len(scope.OwnWrites[a]) != 0 || scope.OwnDeletes[a] != nil {
			scope.StageProjectedColumnRoots(a, nil, nil)
			delete(scope.Stage.Rows, a)
			delete(scope.Stage.Deletes, a)
		}
	}
	if len(stage.initialTables) > 0 {
		if scope.Stage.Rows == nil {
			scope.Stage.Rows = make(map[ids.ObjectAddress][]storage.RowVersion, len(stage.initialTables))
		}
		for a, store := range stage.initialTables {
			scope.Stage.Rows[a] = store.Versions
		}
	}
	if err = e.prepareCatalogOverlayIndexes(scope, stage, changes); err != nil {
		return err
	}
	e.Mu.Lock()
	scope.Op.Session.Mu.Lock()
	err = PublicationOperationLocked(e, scope.Op)
	if err == nil && scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		err = ids.ErrCatalogReplaced
	}
	// Descriptor predecessors retain conflict precedence when an ACL update
	// also advances role authority. Both proofs remain required for transfer.
	if err == nil {
		err = e.CatalogPreflightLocked(changes)
	}
	if err == nil && stage.AuthSession != nil && (stage.AuthSession != scope.Op.Session || scope.Op.Session.Authorization.Revision != stage.AuthRevision || e.OperationRolesLocked(scope.Op).Revision != stage.RoleRevision) {
		err = fmt.Errorf("stuntgres: authorization changed before publication")
	}
	if err == nil {
		err = e.AssignTransactionXIDLocked(scope.TxRef)
	}
	if err == nil {
		scope.Catalog = changes
		for address, removal := range stage.Removals() {
			if !removal.Internal {
				scope.RecordEventDrop(address)
			}
		}
		stage.Lifecycle = catalogPublished
	}
	scope.Op.Session.Mu.Unlock()
	e.Mu.Unlock()
	return err
}

func (e *Engine) stageCatalogChangesLocked(stage *CatalogStage, changes *session.Journal) {
	changes.RecordCandidateDefaults(e.Catalog.State, &stage.Candidate)
	if stage.RoleCandidateRecord != nil {
		// captureCatalogStageLocked validated this immutable candidate. A later
		// descriptor-only stage must retain, not clear, the pending role root.
		changes.Roles = stage.RoleCandidateRecord
	}
	changes.RecordRowShapes(stage.RowShapes, &stage.Candidate)
	if r := stage.MaterializedOrZero().Replacement; r != nil {
		a := r.Original.Address()
		changes.RecordMaterializedRefresh(session.MaterializedRefresh{Address: a, Predecessor: r.Predecessor, Storage: r.Storage, Ends: r.Ends, Generation: r.Generation, Descriptor: r.Descriptor, Concurrent: r.Concurrent, Committed: r.Predecessor == e.Storage.Relations[a], Frontier: e.Visibility.CommitSeq()})
	}
	for a, d := range stage.Descriptors() {
		e.CatalogChangeLocked(changes, a, d)
		if table, ok := d.(*catalog.Relation); ok && table.RelationKind.Stored() && e.PrivateStorageLocked(changes, a) == nil {
			changes.Storage[a] = session.StorageChange{Replacement: &storage.Relation{}, Relation: table}
		}
	}
	for address, candidate := range stage.IndexesOrZero().Candidates {
		if index.JournalIndexesOrZero(changes).Candidates == nil {
			index.JournalIndexes(changes).Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
		}
		if index.JournalIndexesOrZero(changes).UniqueRoots == nil {
			index.JournalIndexes(changes).UniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot)
		}
		index.JournalIndexes(changes).Candidates[address], index.JournalIndexes(changes).UniqueRoots[address] = candidate, candidate.Root
	}
	for a := range stage.Removals() {
		e.CatalogChangeLocked(changes, a, nil)
		delete(changes.Generators, a)
		delete(changes.Storage, a)
		index.JournalOf(changes.Indexes).Forget(a)
		delete(index.JournalIndexesOrZero(changes).Rebuilds, a)
	}
	for a, g := range stage.Sequence.StagedGenerators() {
		changes.Generators[a] = g
	}
}

// DiscardRemovedWrites retains complete private roots for surviving relations,
// including the current callback suffix, and removes roots retired by stage.
func (s *StatementScope) DiscardRemovedWrites(stage *CatalogStage) {
	s.projectPrivateRoots()
	rows, deletes := s.Stage.Rows, s.Stage.Deletes
	s.Stage.Rows = make(map[ids.ObjectAddress][]storage.RowVersion, len(rows))
	for a, values := range rows {
		if _, removed := stage.Removal(a); !removed {
			s.Stage.Rows[a] = values
		}
	}
	s.Stage.Deletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes, len(deletes))
	for a, root := range deletes {
		if _, removed := stage.Removal(a); !removed {
			s.Stage.Deletes[a] = root
		}
	}
}
