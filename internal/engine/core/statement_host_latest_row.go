package core

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// LatestRow resolves the original exact candidate, then admits any differently
// keyed genuine successor unlocked and repeats the canonical read after waiting.
func (h *statementHost) LatestRow(r dml.RowRequest) (dml.LatestRow, error) {
	s := h.scope()
	e := s.Engine()
	var additional lock.Resource
	for {
		e.Mu.Lock()
		latest, err := h.latestRowLocked(r)
		if err != nil || !latest.Live {
			e.Mu.Unlock()
			return latest, err
		}
		ref := r.Ref
		if r.Kind == dml.RowDelete {
			// DELETE's captured heap, not the source reference's epoch, owns
			// latest-row authority. After that gate, locks use the current
			// incarnation; tupleResourceLocked still validates domain/TxRef.
			ref.Epoch = e.Catalog.State.Epoch()
		}
		resource, err := s.tupleResourceLocked(ref, latest.Version, r.Storage)
		identity := storage.RowVersion{Row: r.Row, Version: r.Physical}
		if err == nil && (resource == s.admittedTuple(ref, identity) || resource == additional) {
			s.rememberTuple(ref, latest.Version, resource)
			e.Mu.Unlock()
			return latest, nil
		}
		e.Mu.Unlock()
		if err != nil {
			return dml.LatestRow{}, err
		}
		mode := r.GrantMode
		if mode == 0 {
			mode = lock.ForNoKeyUpdate
		}
		if err := s.acquireUpdateRowMode(ref, latest.Version, mode); err != nil {
			return dml.LatestRow{}, err
		}
		additional = s.admittedTuple(ref, latest.Version)
	}
}

func (h *statementHost) latestRowLocked(r dml.RowRequest) (dml.LatestRow, error) {
	s := h.scope()
	e := s.Engine()
	if err := s.ValidateLockedRelation(e); err != nil {
		return dml.LatestRow{}, err
	}
	if r.Storage != nil && r.Storage != e.PrivateStorageLocked(s.PendingCatalog(), r.Ref.Address) {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
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
		Store:         e.PrivateStorageLocked(s.PendingCatalog(), address),
		OwnWrites:     s.OwnWrites[address],
		OwnDeletes:    s.OwnDeletes[address],
		StagedWrites:  s.Stage.Rows[address],
		StagedDeletes: s.Stage.Deletes[address],
		CommitSeq:     e.Visibility.CommitSeq(),
		Stable:        s.Owner.Isolation.Stable(),
		Frontier:      s.Owner.Frontier,
	}
}

// latestEpochsCurrentLocked reports whether both the request's and the
// transaction's catalog epochs are current, as UPDATE and MERGE need.
func (s *StatementScope) latestEpochsCurrentLocked(e *Engine, r dml.RowRequest) bool {
	return r.Ref.Epoch == e.Catalog.State.Epoch() && s.TxRef.Epoch() == e.Catalog.State.Epoch()
}

// Committed and private hits retain exact physical identity; current-command
// replacements precede prior writes through the common successor reader.
func (s *StatementScope) latestConflictUpdateRowLocked(e *Engine, r dml.RowRequest) (dml.LatestRow, error) {
	ref := r.Ref
	if ref.Epoch != e.Catalog.State.Epoch() {
		return dml.LatestRow{}, ids.ErrCatalogReplaced
	}
	catalog := e.PrivateCatalogLocked(s.PendingCatalog())
	parent, ok := catalog.Relation(ref.Address)
	store := e.PrivateStorageLocked(s.PendingCatalog(), ref.Address)
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
	latest, err := dml.LatestUpdateRow(dml.LatestRowSource{Store: store, OwnWrites: s.OwnWrites[ref.Address], StagedWrites: s.Stage.Rows[ref.Address], OwnDeletes: s.OwnDeletes[ref.Address], StagedDeletes: s.Stage.Deletes[ref.Address], CommitSeq: e.Visibility.CommitSeq(), Stable: s.Owner.Isolation.Stable(), Frontier: s.Owner.Frontier}, r)
	if err != nil {
		return dml.LatestRow{}, err
	}
	latest.Parent, latest.Schema = parent, namespace.Name()
	return latest, nil
}
