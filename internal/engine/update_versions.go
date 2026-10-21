package engine

import (
	"errors"
	"slices"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Conflict UPDATE separately proves cardinality through its affected-row set.
func (w *WriteStage) replaceDistinct(ref ids.ObjectRef, old []storage.RowVersion, rows index.WriteBatch) error {
	return w.replaceRows(ref, old, rows, true, true)
}

func (w *WriteStage) replace(ref ids.ObjectRef, old []storage.RowVersion, rows index.WriteBatch) error {
	return w.replaceRows(ref, old, rows, false, false)
}

// Ordinary UPDATE proves each accepted physical target distinct through its
// frozen scan / FROM winner gate, and excludes existing staged RowIDs before
// choosing this append lane. Ordinary target and RI validation still applies.
func (w *WriteStage) appendUpdateReplacement(ref ids.ObjectRef, old []storage.RowVersion, rows index.WriteBatch) error {
	return w.replaceRows(ref, old, rows, false, true)
}

func (w *WriteStage) replaceRows(ref ids.ObjectRef, old []storage.RowVersion, batch index.WriteBatch, conflictDistinct, appendOnly bool) (err error) {
	rows := batch.Values
	if w == nil || w.self != w || w.scope == nil {
		return errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	defer func() {
		if err == nil {
			err = s.flushPhysicalEvents()
		} else {
			s.multiEvents = nil
		}
	}()
	if err := s.active(); err != nil {
		return err
	}
	if ref != s.lockedRef && s.mutationStorage(ref) != nil {
		if err := s.validateInheritedReplacements(ref, old); err != nil {
			return err
		}
	}
	if s.foreignActionAuth != nil {
		admission := s.foreignActionsOrZero().Admissions[ref.Address]
		if admission == nil || !admission.Writable {
			return errors.New("stuntgres: missing writable internal action admission")
		}
		var small [8]ids.RowID
		if s.engine().locks.MissingRowGrant(s.ticket, ref, dml.VersionRows(small[:0], old)) >= 0 {
			return errors.New("stuntgres: internal action replacement requires its row grant")
		}
	}
	if len(old) != len(rows) {
		return errors.New("stuntgres: invalid replacement shape")
	}
	var smallModes [1]lock.MultiMode
	var modes []lock.MultiMode
	if batch.ActionMode == 0 {
		modes = smallModes[:min(len(old), 1)]
		if len(old) > 1 {
			modes = make([]lock.MultiMode, len(old))
		}
	}
	for i, predecessor := range old {
		mode := batch.ActionMode
		if mode == 0 {
			changed, failure := s.uniqueKeyChanged(ref, predecessor.Values, rows[i])
			if failure != nil {
				return failure
			}
			mode = lock.MultiNoKeyAction
			if changed {
				mode = lock.MultiUpdateAction
			}
			modes[i] = mode
		}
		if mode != lock.MultiNoKeyAction && mode != lock.MultiUpdateAction {
			return errors.New("stuntgres: invalid replacement action intent")
		}
		grant := lock.ForNoKeyUpdate
		if mode == lock.MultiUpdateAction {
			grant = lock.ForUpdate
		}
		if err := s.acquireUpdateRowMode(ref, predecessor.Row, grant); err != nil {
			return err
		}
	}
retry:
	if err := s.validateIndexCandidates(ref, &batch); err != nil {
		return err
	}
	s.owner.Mu.Lock()
	e := s.engine()
	e.mu.Lock()
	admission := e.privateCatalogLocked(s.owner.Catalog).State()
	if binding := s.uniqueBinding; binding != nil && binding.Catalog != admission {
		e.mu.Unlock()
		s.owner.Mu.Unlock()
		if err := s.revalidateIndexRows(ref, old, &batch); err != nil {
			return err
		}
		goto retry
	}
	defer s.owner.Mu.Unlock()
	defer e.mu.Unlock()
	if !s.worked || s.owner.State == session.TxIdle || s.owner.State == session.TxAborted {
		return errors.New("stuntgres: inactive write stage")
	}
	if ref.Domain() != e.catalog.Domain {
		return errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	if s.foreignActionAuth != nil || !conflictDistinct && s.mutationStorage(ref) == nil && s.partitionRelations[ref.Address] == nil {
		if err := s.validateForeignActionLocked(e, ref); err != nil {
			return err
		}
	}
	relation, ok := e.privateCatalogLocked(s.owner.Catalog).Relation(ref.Address)
	if !ok || e.privateStorageLocked(s.owner.Catalog, ref.Address) == nil || len(old) != len(rows) {
		return errors.New("stuntgres: invalid update stage")
	}
	for i, row := range rows {
		if old[i].Row == 0 || old[i].Version == 0 || len(row) != len(relation.Columns) {
			return errors.New("stuntgres: invalid staged row")
		}
	}
	if conflictDistinct {
		if s.insertConflict == nil || s.insertConflict.Update == nil {
			return errors.New("stuntgres: distinct replacement without conflict update")
		}
		for _, predecessor := range old {
			if _, affected := s.insertConflict.AffectedRows[predecessor.Row]; affected {
				return dml.ConflictUpdateCardinalityError()
			}
		}
	}
	if uint64(len(rows)) > ^uint64(0)-e.storage.VersionFrontier() {
		return errors.New("stuntgres: version identity exhausted")
	}
	var versions, distinctRows []storage.RowVersion
	if appendOnly {
		prefix := w.rows[ref.Address]
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
		versions[i] = storage.RowVersion{Row: predecessor.Row, Version: e.storage.AllocateVersionLocked(), Base: base, Values: row, KeyChangedAt: predecessor.KeyChangedAt, PendingKeyChange: predecessor.PendingKeyChange, IndexAdmission: admission}
	}
	if batch.States != nil {
		for i := range versions {
			versions[i].IndexOutputs = batch.States[i].Indexes
		}
	}
	if e.visibility.RelevantSerializableLocked(s.txRef) != nil {
		for i, row := range versions {
			predecessor := old[i]
			event := [1]visibility.SerializableWrite{{Row: row.Row, Version: row.Version, Base: predecessor.Version, BaseBorn: predecessor.Born, Creator: e.visibility.SerializableCreatorLocked(predecessor.Version)}}
			if err := e.serializableWriteLocked(s, ref, event[:]); err != nil {
				return err
			}
		}
	}
	if len(versions) != 0 {
		if w.rows == nil {
			w.rows = make(map[ids.ObjectAddress][]storage.RowVersion)
		}
		if appendOnly {
			w.rows[ref.Address] = distinctRows
		} else {
			w.rows[ref.Address] = mergePrivateRows(w.rows[ref.Address], versions, true)
		}
		w.replacing = true
		for i, next := range versions {
			mode := batch.ActionMode
			if mode == 0 {
				mode = modes[i]
			}
			s.multiEvents = append(s.multiEvents, lock.MultiEvent{Tuple: lock.MultiTuple{lock.Resource{ObjectRef: ref, Row: old[i].Row}, old[i].Version}, Mode: mode, Successor: next.Version})
			s.recordForeignActionReplacement(ref, old[i], next)
		}
	}
	return nil
}
