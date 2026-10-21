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
		return s.latestUpdateRowLocked(e, r)
	case dml.RowDelete:
		return s.latestDeleteRowLocked(e, r)
	case dml.RowMerge:
		return s.latestMergeRowLocked(e, r)
	case dml.RowConflictUpdate:
		return s.latestConflictUpdateRowLocked(e, r)
	}
	return dml.LatestRow{}, errors.New("stuntgres: invalid latest row kind")
}

// An UPDATE predecessor needs both epochs current and conflicts under a
// retained snapshot.
func (s *StatementScope) latestUpdateRowLocked(e *Engine, r dml.RowRequest) (dml.LatestRow, error) {
	if r.Ref.Epoch != e.catalog.state.Epoch() || s.txRef.Epoch() != e.catalog.state.Epoch() {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	store := e.privateStorageLocked(s.owner.Catalog, r.Ref.Address)
	if store == nil {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	current := store.Versions
	if r.Own {
		current = s.ownWrites[r.Ref.Address]
	}
	row, _, found := storage.RowByID(current, r.Row, r.Hint)
	if !found {
		return dml.LatestRow{}, errors.New("stuntgres: invalid update predecessor")
	}
	if err := s.repeatableConflict(row, store.Ends); err != nil {
		return dml.LatestRow{}, err
	}
	return dml.LatestRow{Version: row, Live: visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[r.Ref.Address])}, nil
}

// Row arbitration freezes identity, not a stale ordinal or pre-wait predicate.
func (s *StatementScope) latestDeleteRowLocked(e *Engine, r dml.RowRequest) (dml.LatestRow, error) {
	store := e.privateStorageLocked(s.owner.Catalog, r.Ref.Address)
	if store == nil || store != r.Storage {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	rows := store.Versions
	if r.Own {
		rows = s.ownWrites[r.Ref.Address]
	}
	row, _, found := storage.RowByID(rows, r.Row, r.Hint)
	if !found {
		return dml.LatestRow{}, errors.New("stuntgres: invalid deletion predecessor")
	}
	if err := s.repeatableConflict(row, store.Ends); err != nil {
		return dml.LatestRow{}, err
	}
	return dml.LatestRow{Version: row, Live: visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[r.Ref.Address])}, nil
}

// MERGE follows a committed update even with a retained RR frontier. Deletion
// still conflicts; query visibility, not this fetch, owns old+own occurrences.
func (s *StatementScope) latestMergeRowLocked(e *Engine, r dml.RowRequest) (dml.LatestRow, error) {
	if r.Ref.Epoch != e.catalog.state.Epoch() || s.txRef.Epoch() != e.catalog.state.Epoch() {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	store := e.privateStorageLocked(s.owner.Catalog, r.Ref.Address)
	if store == nil {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	current, hint := store.Versions, r.Hint
	if own, _, found := storage.RowByID(s.ownWrites[r.Ref.Address], r.Row, -1); found {
		return dml.LatestRow{Version: own, Live: visibility.LiveVersion(own, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[r.Ref.Address])}, nil
	}
	row, _, found := storage.RowByID(current, r.Row, hint)
	if !found {
		return dml.LatestRow{}, errors.New("stuntgres: invalid MERGE predecessor")
	}
	live := visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[r.Ref.Address])
	if !live && stableIsolation(s.owner.Isolation) && store.Ends != nil {
		if tombstone, ok := store.Ends.Rows[row.Row]; ok && tombstone.Version == row.Version && tombstone.End > s.owner.Frontier {
			return dml.LatestRow{}, sqlError("40001", "could not serialize access due to concurrent delete")
		}
	}
	return dml.LatestRow{Version: row, Live: live}, nil
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
	if err := s.repeatableConflict(row, store.Ends); err != nil {
		return dml.LatestRow{}, err
	}
	live := visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[ref.Address]) && !index.UniqueRowDeleted(row.Row, s.stage.deletes[ref.Address])
	return dml.LatestRow{Version: row, Live: live, Parent: parent, Schema: namespace.Name()}, nil
}
