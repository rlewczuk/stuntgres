package engine

import (
	"errors"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// A captured physical position is only a hint; arbitration is by stable RowID.
func rowByID(rows []storage.RowVersion, id ids.RowID, hint int) (storage.RowVersion, int, bool) {
	if hint >= 0 && hint < len(rows) && rows[hint].Row == id {
		return rows[hint], hint, true
	}
	for i, row := range rows {
		if row.Row == id {
			return row, i, true
		}
	}
	return storage.RowVersion{}, 0, false
}

// A replacement merge never changes a prefix retained by an earlier view.
// Insert-only commands retain the existing amortized append path.
func mergePrivateRows(old, stage []storage.RowVersion, replacing bool) []storage.RowVersion {
	if len(old) == 0 {
		return stage
	}
	if !replacing {
		return append(old, stage...)
	}
	next := make([]storage.RowVersion, len(old), len(old)+len(stage))
	copy(next, old)
	if len(stage) == 1 {
		for i, row := range old {
			if row.Row == stage[0].Row {
				next[i] = stage[0]
				return next
			}
		}
		return append(next, stage[0])
	}
	slots := make(map[ids.RowID]int, len(old))
	for i, row := range old {
		slots[row.Row] = i
	}
	for _, row := range stage {
		if i, ok := slots[row.Row]; ok {
			next[i] = row
		} else {
			slots[row.Row] = len(next)
			next = append(next, row)
		}
	}
	return next
}

// Conflict UPDATE separately proves cardinality through its affected-row set.
func (w *WriteStage) replaceDistinct(ref ids.ObjectRef, old []storage.RowVersion, rows indexWriteBatch) error {
	return w.replaceRows(ref, old, rows, true, true)
}

func (w *WriteStage) replace(ref ids.ObjectRef, old []storage.RowVersion, rows indexWriteBatch) error {
	return w.replaceRows(ref, old, rows, false, false)
}

// Ordinary UPDATE proves each accepted physical target distinct through its
// frozen scan / FROM winner gate, and excludes existing staged RowIDs before
// choosing this append lane. Ordinary target and RI validation still applies.
func (w *WriteStage) appendUpdateReplacement(ref ids.ObjectRef, old []storage.RowVersion, rows indexWriteBatch) error {
	return w.replaceRows(ref, old, rows, false, true)
}

func (w *WriteStage) replaceRows(ref ids.ObjectRef, old []storage.RowVersion, batch indexWriteBatch, conflictDistinct, appendOnly bool) (err error) {
	rows := batch.values
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
		admission := s.foreignActionsOrZero().admissions[ref.Address]
		if admission == nil || !admission.writable {
			return errors.New("stuntgres: missing writable internal action admission")
		}
		var small [8]ids.RowID
		if s.engine().locks.MissingRowGrant(s.ticket, ref, versionRows(small[:0], old)) >= 0 {
			return errors.New("stuntgres: internal action replacement requires its row grant")
		}
	}
	if len(old) != len(rows) {
		return errors.New("stuntgres: invalid replacement shape")
	}
	var smallModes [1]lock.MultiMode
	var modes []lock.MultiMode
	if batch.actionMode == 0 {
		modes = smallModes[:min(len(old), 1)]
		if len(old) > 1 {
			modes = make([]lock.MultiMode, len(old))
		}
	}
	for i, predecessor := range old {
		mode := batch.actionMode
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
	s.owner.mu.Lock()
	e := s.engine()
	e.mu.Lock()
	admission := e.privateCatalogLocked(s.owner.catalog).State()
	if binding := s.uniqueBinding; binding != nil && binding.catalog != admission {
		e.mu.Unlock()
		s.owner.mu.Unlock()
		if err := s.revalidateIndexRows(ref, old, &batch); err != nil {
			return err
		}
		goto retry
	}
	defer s.owner.mu.Unlock()
	defer e.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
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
	relation, ok := e.privateCatalogLocked(s.owner.catalog).Relation(ref.Address)
	if !ok || e.privateStorageLocked(s.owner.catalog, ref.Address) == nil || len(old) != len(rows) {
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
			if _, affected := s.insertConflict.affectedRows[predecessor.Row]; affected {
				return conflictUpdateCardinalityError()
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
		if identity := batch.candidate(i).exclusion; identity != nil {
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
	if batch.states != nil {
		for i := range versions {
			versions[i].IndexOutputs = batch.states[i].indexes
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
			mode := batch.actionMode
			if mode == 0 {
				mode = modes[i]
			}
			s.multiEvents = append(s.multiEvents, lock.MultiEvent{Tuple: lock.MultiTuple{lock.Resource{ObjectRef: ref, Row: old[i].Row}, old[i].Version}, Mode: mode, Successor: next.Version})
			s.recordForeignActionReplacement(ref, old[i], next)
		}
	}
	return nil
}
