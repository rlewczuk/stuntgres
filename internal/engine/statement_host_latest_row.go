package engine

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// LatestRow takes Engine.mu once, validates the locked relations and reads
// the requested row with its kind's gate, row source, isolation policy and
// errors. It never waits.
func (h *statementHost) LatestRow(r dml.RowRequest) (dml.LatestRow, error) {
	s := h.scope()
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return dml.LatestRow{}, err
	}
	switch r.Kind {
	case dml.RowUpdate:
		if !s.latestEpochsCurrentLocked(e, r) {
			return dml.LatestRow{}, ids.ErrCatalogReplaced
		}
		return dml.LatestUpdateRow(s.latestRowSourceLocked(e, r.Ref.Address), r)
	case dml.RowDelete:
		return dml.LatestDeleteRow(s.latestRowSourceLocked(e, r.Ref.Address), r)
	case dml.RowMerge:
		if !s.latestEpochsCurrentLocked(e, r) {
			return dml.LatestRow{}, ids.ErrCatalogReplaced
		}
		return dml.LatestMergeRow(s.latestRowSourceLocked(e, r.Ref.Address), r)
	case dml.RowConflictUpdate:
		return s.latestConflictUpdateRowLocked(e, r)
	}
	return dml.LatestRow{}, errors.New("stuntgres: invalid latest row kind")
}

// latestRowSourceLocked captures the latest-row inputs of address for the
// dml readers. Caller holds Engine.mu.
func (s *StatementScope) latestRowSourceLocked(e *Engine, address ids.ObjectAddress) dml.LatestRowSource {
	return dml.LatestRowSource{
		Store:      e.privateStorageLocked(s.owner.Catalog, address),
		OwnWrites:  s.ownWrites[address],
		OwnDeletes: s.ownDeletes[address],
		CommitSeq:  e.visibility.CommitSeq(),
		Stable:     s.owner.Isolation.Stable(),
		Frontier:   s.owner.Frontier,
	}
}

// latestEpochsCurrentLocked reports whether both the request's and the
// transaction's catalog epochs are current, as UPDATE and MERGE need.
func (s *StatementScope) latestEpochsCurrentLocked(e *Engine, r dml.RowRequest) bool {
	return r.Ref.Epoch == e.catalog.state.Epoch() && s.txRef.Epoch() == e.catalog.state.Epoch()
}

// Committed hits retain their provider's exact physical hint. Private overlays
// are keyed by RowID, with this command's replacement ahead of prior writes.
func (s *StatementScope) latestConflictUpdateRowLocked(e *Engine, r dml.RowRequest) (dml.LatestRow, error) {
	ref := r.Ref
	if ref.Epoch != e.catalog.state.Epoch() {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	catalog := e.privateCatalogLocked(s.owner.Catalog)
	parent, ok := catalog.Relation(ref.Address)
	store := e.privateStorageLocked(s.owner.Catalog, ref.Address)
	if !ok || store == nil || !index.SameRelationRowShape(r.Parent, parent) {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	auth, err := s.writeAuthorizationLocked(e)
	if err != nil {
		return dml.LatestRow{}, err
	}
	auth.Catalog = catalog
	auth.Recapture(auth.Catalog)
	if err := analyze.CheckBinding(auth, dml.BindingForConflict(ref, parent, s.InsertConflict), &parser.Insert{}); err != nil {
		return dml.LatestRow{}, err
	}
	namespace, ok := catalog.Namespace(parent.Namespace())
	if !ok {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	var row storage.RowVersion
	var found bool
	if !r.Own {
		row, _, found = storage.RowByID(store.Versions, r.Row, r.Hint)
	} else {
		if slot, ok := s.InsertConflict.AffectedRows[r.Row]; ok {
			row, found = s.stage.rows[ref.Address][slot], true
		}
		if !found {
			row, found = dml.InsertOwnRow(s.writeHost(), ref, r.Row)
		}
	}
	if !found {
		return dml.LatestRow{}, errors.New("stuntgres: invalid conflict update predecessor")
	}
	if err := visibility.RepeatableConflict(s.owner.Isolation.Stable(), s.owner.Frontier, row, store.Ends); err != nil {
		return dml.LatestRow{}, err
	}
	live := visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[ref.Address]) && !index.UniqueRowDeleted(row.Row, s.stage.deletes[ref.Address])
	return dml.LatestRow{Version: row, Live: live, Parent: parent, Schema: namespace.Name()}, nil
}
