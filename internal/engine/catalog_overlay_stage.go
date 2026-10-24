package engine

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
func (e *Engine) stageCatalogOverlay(scope *StatementScope, stage *CatalogStage, changes *session.Journal) error {
	e.mu.Lock()
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if stage.initialTx != (ids.TxRef{}) && stage.initialTx != scope.txRef {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	err := e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(scope.owner.Catalog).State(), scope.catalogAuthority)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if stage.RemovalCount() > 0 {
		scope.discardRemovedWrites(stage)
	}
	if err = scope.projectCatalogOverlayShapes(stage, changes); err != nil {
		return err
	}
	if err = scope.reownTypeOverlayRows(stage, changes); err != nil {
		return err
	}
	if r := stage.materializedOrZero().Replacement; r != nil {
		// A private CREATE WITH DATA has own rows on the old empty store.
		// REFRESH replaces that complete target, not an append to those rows.
		a := r.Original.Address()
		if len(scope.ownWrites[a]) != 0 || scope.ownDeletes[a] != nil {
			scope.stageProjectedColumnRoots(a, nil, nil)
			delete(scope.stage.rows, a)
			delete(scope.stage.deletes, a)
		}
	}
	if len(stage.initialTables) > 0 {
		if scope.stage.rows == nil {
			scope.stage.rows = make(map[ids.ObjectAddress][]storage.RowVersion, len(stage.initialTables))
		}
		for a, store := range stage.initialTables {
			scope.stage.rows[a] = store.Versions
		}
	}
	if err = e.prepareCatalogOverlayIndexes(scope, stage, changes); err != nil {
		return err
	}
	e.mu.Lock()
	scope.op.Session.Mu.Lock()
	err = publicationOperationLocked(e, scope.op)
	if err == nil && scope.txRef.Epoch() != e.catalog.state.Epoch() {
		err = ids.ErrCatalogReplaced
	}
	if err == nil && stage.authSession != nil && (stage.authSession != scope.op.Session || scope.op.Session.Authorization.Revision != stage.authRevision || e.operationRolesLocked(scope.op).Revision != stage.roleRevision) {
		err = fmt.Errorf("stuntgres: authorization changed before publication")
	}
	if err == nil {
		err = e.catalogPreflightLocked(changes)
	}
	if err == nil {
		err = e.assignTransactionXIDLocked(scope.txRef)
	}
	if err == nil {
		scope.catalog = changes
		stage.lifecycle = catalogPublished
	}
	scope.op.Session.Mu.Unlock()
	e.mu.Unlock()
	return err
}

func (e *Engine) stageCatalogChangesLocked(stage *CatalogStage, changes *session.Journal) {
	changes.RecordCandidateDefaults(e.catalog.state, &stage.Candidate)
	if stage.roleCandidate != nil {
		// captureCatalogStageLocked validated this immutable candidate. A later
		// descriptor-only stage must retain, not clear, the pending role root.
		changes.Roles = stage.roleCandidate
	}
	for a, rowShape := range stage.rowShapes {
		shape, exists := changes.Storage[a]
		if !exists {
			shape = session.StorageChange{Original: rowShape.Original, Root: rowShape.Root, Ends: rowShape.ObservedEnds, Generation: rowShape.Generation}
		}
		if shape.Replacement != rowShape.Replacement {
			shape.Truncated = false
			shape.MaterializedConcurrent, shape.MaterializedContinuous = false, false
		}
		shape.Replacement = rowShape.Replacement
		if rowShape.Rewritten {
			shape.Rewritten, shape.Frontier = true, rowShape.Frontier
			shape.Snapshot, shape.HasSnapshot = nil, false
		}
		shape.Relation, _ = stage.Get(a).(*catalog.Relation)
		if shape.Relation == nil {
			// An index-expression-only domain generation changes executable
			// index authority without changing its parent's column descriptor.
			shape.Relation = stage.Base.MustRelation(a)
		}
		changes.Storage[a] = shape
	}
	if r := stage.materializedOrZero().Replacement; r != nil {
		a := r.Original.Address()
		shape, exists := changes.Storage[a]
		if !exists {
			shape = session.StorageChange{Original: r.Predecessor, Root: r.Predecessor.Versions, Ends: r.Ends, Generation: r.Generation}
		}
		if !exists {
			shape.MaterializedContinuous = r.Concurrent && r.Predecessor == e.storage.Relations[a]
		} else {
			shape.MaterializedContinuous = shape.MaterializedContinuous && r.Concurrent
		}
		shape.MaterializedConcurrent = r.Concurrent
		// Keep the first committed predecessor (or genuine private CREATE
		// absence). A repeated refresh replaces only the latest private root.
		shape.Replacement, shape.Relation = r.Storage, r.Descriptor
		shape.Truncated = false
		shape.Rewritten, shape.Frontier = true, e.visibility.CommitSeq()
		shape.Snapshot, shape.HasSnapshot = nil, false
		changes.Storage[a] = shape
	}
	for a, d := range stage.Descriptors() {
		e.catalogChangeLocked(changes, a, d)
		if table, ok := d.(*catalog.Relation); ok && table.RelationKind.Stored() && e.privateStorageLocked(changes, a) == nil {
			changes.Storage[a] = session.StorageChange{Replacement: &storage.Relation{}, Relation: table}
		}
	}
	for address, candidate := range stage.indexesOrZero().Candidates {
		if index.JournalIndexesOrZero(changes).Candidates == nil {
			index.JournalIndexes(changes).Candidates = make(map[ids.ObjectAddress]index.RootCandidate)
		}
		if index.JournalIndexesOrZero(changes).UniqueRoots == nil {
			index.JournalIndexes(changes).UniqueRoots = make(map[ids.ObjectAddress]*storage.IndexRoot)
		}
		index.JournalIndexes(changes).Candidates[address], index.JournalIndexes(changes).UniqueRoots[address] = candidate, candidate.Root
	}
	for a := range stage.Removals() {
		e.catalogChangeLocked(changes, a, nil)
		delete(changes.Generators, a)
		delete(changes.Storage, a)
		index.JournalOf(changes.Indexes).Forget(a)
		delete(index.JournalIndexesOrZero(changes).Rebuilds, a)
	}
	for a, g := range stage.sequence.StagedGenerators() {
		changes.Generators[a] = g
	}
}

func (s *StatementScope) discardRemovedWrites(stage *CatalogStage) {
	rows, deletes := s.ownWrites, s.ownDeletes
	if s.projectedRoots {
		rows, deletes = s.stage.rows, s.stage.deletes
	}
	s.projectedRoots = true
	s.stage.rows = make(map[ids.ObjectAddress][]storage.RowVersion, len(rows))
	for a, values := range rows {
		if _, removed := stage.Removal(a); !removed {
			s.stage.rows[a] = values
		}
	}
	s.stage.deletes = make(map[ids.ObjectAddress]*visibility.PrivateDeletes, len(deletes))
	for a, root := range deletes {
		if _, removed := stage.Removal(a); !removed {
			s.stage.deletes[a] = root
		}
	}
}
