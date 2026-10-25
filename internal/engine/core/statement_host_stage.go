package core

import (
	"errors"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// The write stage windows of dml.Stage. Each takes the owner mutex and then
// Engine.mu, never waits, and runs on the scope's own stage; the callers in
// update_versions.go, transaction.go and insert_returning.go keep the work
// around them.

// Active is the statement's activity check, before any candidate
// validation or window.
func (h *statementHost) Active() error { return h.scope().Active() }

// ReadmitRevalidation is the authority step of a revalidation after a stale
// unique binding: one Engine.mu window captures the write authority (the
// routine authority unless an FK action acts) on current's catalog, then the
// prepared and prior's index programs are readmitted unlocked. Catalog-drift
// staging can refresh grants and immutable providers, but cannot
// manufacture a new checked insertion occurrence after the producer finished.
func (h *statementHost) ReadmitRevalidation(prior, current *index.UniqueWriteBinding) error {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	auth, captureErr := s.writeAuthorizationLocked(e)
	if captureErr == nil && s.ForeignActionAuth == nil {
		auth = s.RoutineAuth // Rule relation ownership is not routine authority.
	}
	if captureErr == nil {
		auth.Recapture(e.Catalog.Read(current.Catalog))
	}
	e.Mu.Unlock()
	if captureErr != nil {
		return captureErr
	}
	// Reconciliation checks actual surviving and folded calls, even when
	// a false predicate never initialized the executable key graph.
	if err := s.readmitPreparedPrograms(auth); err != nil {
		return err
	}
	if s.IndexBindings[prior.Ref.Address] != prior {
		if err := prior.ReadmitPrograms(RoutineAccessOf(auth)); err != nil {
			return err
		}
	}
	return nil
}

// PrepareReplacement checks the stage authority, the active statement,
// inherited replacements and an internal FK action's admission and row
// grants before a replacement. It takes no engine lock.
func (h *statementHost) PrepareReplacement(ref ids.ObjectRef, old []storage.RowVersion) error {
	s := h.scope()
	if w := &s.Stage; w.self != w || w.scope == nil {
		return errors.New("stuntgres: foreign or missing write stage")
	}
	if err := s.Active(); err != nil {
		return err
	}
	if ref != s.LockedRef && s.mutationStorage(ref) != nil {
		if err := s.validateInheritedReplacements(ref, old); err != nil {
			return err
		}
	}
	if s.ForeignActionAuth != nil {
		admission := s.foreignActionsOrZero().Admissions[ref.Address]
		if admission == nil || !admission.Writable {
			return errors.New("stuntgres: missing writable internal action admission")
		}
		var small [8]ids.RowID
		if s.Engine().Locks.MissingRowGrant(s.LockTicket, ref, dml.VersionRows(small[:0], old)) >= 0 {
			return errors.New("stuntgres: internal action replacement requires its row grant")
		}
	}
	return nil
}

// StageReplacement is the replacement window. The physical row events it
// queues are flushed after unlocking on success and dropped on failure.
func (h *statementHost) StageReplacement(ref ids.ObjectRef, old []storage.RowVersion, batch index.WriteBatch, modes []lock.MultiMode, lane dml.ReplaceLane) (stale bool, err error) {
	s := h.scope()
	w := &s.Stage
	rows := batch.Values
	conflictDistinct, appendOnly := lane == dml.ReplaceConflict, lane != dml.ReplaceMerge
	defer func() {
		if err == nil {
			err = s.flushPhysicalEvents()
		} else {
			s.multiEvents = nil
		}
	}()
	s.Owner.Mu.Lock()
	e := s.Engine()
	e.Mu.Lock()
	admission := e.PrivateCatalogLocked(s.Owner.Catalog).State()
	if binding := s.UniqueBinding; binding != nil && binding.Catalog != admission {
		e.Mu.Unlock()
		s.Owner.Mu.Unlock()
		return true, nil
	}
	defer s.Owner.Mu.Unlock()
	defer e.Mu.Unlock()
	if !s.worked || s.Owner.State == session.TxIdle || s.Owner.State == session.TxAborted {
		return false, errors.New("stuntgres: inactive write stage")
	}
	if ref.Domain() != e.Catalog.Domain {
		return false, errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return false, ids.ErrCatalogReplaced
	}
	if err := s.ValidateLockedRelation(e); err != nil {
		return false, err
	}
	if s.ForeignActionAuth != nil || !conflictDistinct && s.mutationStorage(ref) == nil && s.PartitionRelations[ref.Address] == nil {
		if err := s.validateForeignActionLocked(e, ref); err != nil {
			return false, err
		}
	}
	relation, ok := e.PrivateCatalogLocked(s.Owner.Catalog).Relation(ref.Address)
	if !ok || e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) == nil || len(old) != len(rows) {
		return false, errors.New("stuntgres: invalid update stage")
	}
	for i, row := range rows {
		if old[i].Row == 0 || old[i].Version == 0 || len(row) != len(relation.Columns) {
			return false, errors.New("stuntgres: invalid staged row")
		}
	}
	if conflictDistinct {
		if s.InsertConflict == nil || s.InsertConflict.Update == nil {
			return false, errors.New("stuntgres: distinct replacement without conflict update")
		}
		for _, predecessor := range old {
			if _, affected := s.InsertConflict.AffectedRows[predecessor.Row]; affected {
				return false, dml.ConflictUpdateCardinalityError()
			}
		}
	}
	if uint64(len(rows)) > ^uint64(0)-e.Storage.VersionFrontier() {
		return false, errors.New("stuntgres: version identity exhausted")
	}
	var versions, distinctRows []storage.RowVersion
	if appendOnly {
		prefix := w.Rows[ref.Address]
		distinctRows = slices.Grow(prefix, len(rows))[:len(prefix)+len(rows)]
		versions = distinctRows[len(prefix):]
	} else {
		versions = make([]storage.RowVersion, len(rows))
	}
	for i, row := range rows {
		predecessor := old[i]
		if identity := batch.Candidate(i).Exclusion; identity != nil {
			reserved := *identity
			reserved.KeyChangedAt, reserved.PendingKeyChange = predecessor.KeyChangedAt, predecessor.PendingKeyChange
			reserved.IndexAdmission = admission
			versions[i] = reserved
			continue
		}
		base := predecessor.Base
		if predecessor.Born != 0 {
			base = predecessor.Version
		}
		versions[i] = storage.RowVersion{Row: predecessor.Row, Version: e.Storage.AllocateVersionLocked(), Base: base, Values: row, KeyChangedAt: predecessor.KeyChangedAt, PendingKeyChange: predecessor.PendingKeyChange, IndexAdmission: admission}
	}
	if batch.States != nil {
		for i := range versions {
			versions[i].IndexOutputs = batch.States[i].Indexes
		}
	}
	if e.Visibility.RelevantSerializableLocked(s.TxRef) != nil {
		for i, row := range versions {
			predecessor := old[i]
			event := [1]visibility.SerializableWrite{{Row: row.Row, Version: row.Version, Base: predecessor.Version, BaseBorn: predecessor.Born, Creator: e.Visibility.SerializableCreatorLocked(predecessor.Version)}}
			if err := e.SerializableWriteLocked(s, ref, event[:]); err != nil {
				return false, err
			}
		}
	}
	if len(versions) != 0 {
		if w.Rows == nil {
			w.Rows = make(map[ids.ObjectAddress][]storage.RowVersion)
		}
		if appendOnly {
			w.Rows[ref.Address] = distinctRows
		} else {
			w.Rows[ref.Address] = visibility.MergePrivateRows(w.Rows[ref.Address], versions, true)
		}
		w.replacing = true
		relevant := s.foreignEffectRelevant(ref, e.PrivateCatalogLocked(s.Owner.Catalog))
		for i, next := range versions {
			mode := batch.ActionMode
			if mode == 0 {
				mode = modes[i]
			}
			s.multiEvents = append(s.multiEvents, lock.MultiEvent{Tuple: lock.MultiTuple{lock.Resource{ObjectRef: ref, Row: old[i].Row}, old[i].Version}, Mode: mode, Successor: next.Version})
			if relevant {
				s.recordForeignEffect(ref, old[i], next)
			}
		}
	}
	return false, nil
}

// StageInsert is the canonical insert window: SQL producers validate each
// accepted prefix before demanding the next row, and only this window
// transfers the completed immutable batch.
func (h *statementHost) StageInsert(ref ids.ObjectRef, batch index.WriteBatch) (stale bool, err error) {
	s := h.scope()
	w := &s.Stage
	rows := batch.Values
	s.Owner.Mu.Lock()
	e := s.Engine()
	e.Mu.Lock()
	admission := e.PrivateCatalogLocked(s.Owner.Catalog).State()
	if binding := s.UniqueBinding; binding != nil && binding.Catalog != admission {
		e.Mu.Unlock()
		s.Owner.Mu.Unlock()
		return true, nil
	}
	defer s.Owner.Mu.Unlock()
	defer e.Mu.Unlock()
	if !s.worked || s.Owner.State == session.TxIdle || s.Owner.State == session.TxAborted {
		return false, errors.New("stuntgres: inactive write stage")
	}
	if ref.Domain() != e.Catalog.Domain {
		return false, errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.Owner.Ref.Epoch() {
		return false, ids.ErrCatalogReplaced
	}
	if _, update := s.Statement.(*parser.Update); update && s.PartitionInsert != nil {
		if err := s.ValidateLockedRelation(e); err != nil {
			return false, err
		}
		store := s.mutationStorage(ref)
		if store == nil || store != e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) || s.View == nil || s.View.Data().GenerationAt(ref.Address) != store {
			return false, ids.ErrCatalogReplaced
		}
		if !e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: ref}, lock.RowExclusive) {
			return false, errors.New("stuntgres: routed update requires its destination grant")
		}
	}
	if e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) == nil {
		return false, errors.New("stuntgres: relation storage is missing")
	}
	relation, ok := e.PrivateCatalogLocked(s.Owner.Catalog).Relation(ref.Address)
	if !ok {
		return false, errors.New("stuntgres: invalid relation descriptor")
	}
	for _, row := range rows {
		if len(row) != len(relation.Columns) {
			return false, errors.New("stuntgres: invalid staged row width")
		}
	}
	if s.InsertConflict != nil && s.InsertConflict.Update == nil {
		s.InsertConflict.Affected = len(rows)
	}
	if len(rows) == 0 {
		return false, nil
	}
	if err := e.AssignTransactionXIDLocked(s.TxRef); err != nil {
		return false, err
	}
	var versions []storage.RowVersion
	if len(relation.Constraints.Exclusion) != 0 {
		versions = make([]storage.RowVersion, len(rows))
		for i := range rows {
			reserved := batch.Candidate(i).Exclusion
			if reserved == nil {
				return false, errors.New("stuntgres: exclusion row lost its admission identity")
			}
			versions[i] = *reserved
			versions[i].IndexAdmission = admission
		}
	} else {
		versions, err = e.Storage.AllocateRowVersionsLocked(rows, admission)
	}
	if err != nil {
		return false, err
	}
	if batch.States != nil {
		for i := range versions {
			versions[i].IndexOutputs = batch.States[i].Indexes
		}
	}
	if e.Visibility.RelevantSerializableLocked(s.TxRef) != nil {
		for _, row := range versions {
			event := [1]visibility.SerializableWrite{{Row: row.Row, Version: row.Version}}
			if err := e.SerializableWriteLocked(s, ref, event[:]); err != nil {
				return false, err
			}
		}
	}
	if w.Rows == nil {
		w.Rows = make(map[ids.ObjectAddress][]storage.RowVersion)
	}
	if len(w.Rows[ref.Address]) == 0 {
		w.Rows[ref.Address] = versions
	} else {
		w.Rows[ref.Address] = append(w.Rows[ref.Address], versions...)
	}
	if s.foreignEffectRelevant(ref, e.PrivateCatalogLocked(s.Owner.Catalog)) {
		for _, next := range versions {
			s.recordForeignEffect(ref, storage.RowVersion{}, next)
		}
	}
	return false, nil
}

// AppendAcceptedInsert is the same catalog and index authority as StageInsert
// for one RETURNING row, appended to amortized stage storage.
func (h *statementHost) AppendAcceptedInsert(ref ids.ObjectRef, candidate index.WriteCandidate) (accepted, stale bool, err error) {
	s := h.scope()
	w := &s.Stage
	row := candidate.Values
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !s.worked || s.Owner.State == session.TxIdle || s.Owner.State == session.TxAborted {
		return false, false, errors.New("stuntgres: inactive write stage")
	}
	if ref.Domain() != e.Catalog.Domain {
		return false, false, errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.Owner.Ref.Epoch() {
		return false, false, ids.ErrCatalogReplaced
	}
	catalog := e.PrivateCatalogLocked(s.Owner.Catalog)
	if s.UniqueBinding == nil || s.UniqueBinding.Catalog != catalog.State() {
		return false, true, nil
	}
	parent, ok := catalog.Relation(ref.Address)
	if !ok || !index.SameRelationRowShape(s.UniqueBinding.Parent, parent) {
		return false, false, ids.ErrCatalogReplaced
	}
	if e.PrivateStorageLocked(s.Owner.Catalog, ref.Address) == nil {
		return false, false, errors.New("stuntgres: relation storage is missing")
	}
	if len(row) != len(parent.Columns) {
		return false, false, errors.New("stuntgres: invalid staged row width")
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return false, false, err
	}
	auth.Catalog = catalog
	auth.Recapture(auth.Catalog)
	b := s.InsertReturning.Binding
	if b.ViewWrite == nil && s.PartitionInsert == nil {
		b.Ref, b.Relation = ref, parent
	}
	if err = analyze.CheckBinding(auth, b, &parser.Insert{}); err != nil {
		return false, false, err
	}
	var version storage.RowVersion
	if candidate.Exclusion != nil {
		version = *candidate.Exclusion
	} else {
		if e.Storage.RowFrontier() == ^uint64(0) || e.Storage.VersionFrontier() == ^uint64(0) {
			return false, false, errors.New("stuntgres: row or version identity exhausted")
		}
		version = storage.RowVersion{Row: e.Storage.AllocateRowLocked(), Version: e.Storage.AllocateVersionLocked(), Values: row}
	}
	version.IndexAdmission = catalog.State()
	version.IndexOutputs = candidate.Indexes
	if e.Visibility.RelevantSerializableLocked(s.TxRef) != nil {
		events := [1]visibility.SerializableWrite{{Row: version.Row, Version: version.Version}}
		if err = e.SerializableWriteLocked(s, ref, events[:]); err != nil {
			return false, false, err
		}
	}
	if w.Rows == nil {
		w.Rows = make(map[ids.ObjectAddress][]storage.RowVersion)
	}
	w.Rows[ref.Address] = append(w.Rows[ref.Address], version)
	if s.foreignEffectRelevant(ref, catalog) {
		s.recordForeignEffect(ref, storage.RowVersion{}, version)
	}
	return true, false, nil
}

// Delete is WriteStage.Delete on the scope's own stage.
func (h *statementHost) Delete(c visibility.DeleteCandidate) (bool, error) {
	return h.scope().Writes().Delete(c)
}
