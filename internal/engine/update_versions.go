package engine

import (
	"errors"
	"slices"

	"stuntgres/internal/engine/ids"
)

// A captured physical position is only a hint; arbitration is by stable RowID.
func rowByID(rows []rowVersion, id ids.RowID, hint int) (rowVersion, int, bool) {
	if hint >= 0 && hint < len(rows) && rows[hint].row == id {
		return rows[hint], hint, true
	}
	for i, row := range rows {
		if row.row == id {
			return row, i, true
		}
	}
	return rowVersion{}, 0, false
}

// A replacement merge never changes a prefix retained by an earlier view.
// Insert-only commands retain the existing amortized append path.
func mergePrivateRows(old, stage []rowVersion, replacing bool) []rowVersion {
	if len(old) == 0 {
		return stage
	}
	if !replacing {
		return append(old, stage...)
	}
	next := make([]rowVersion, len(old), len(old)+len(stage))
	copy(next, old)
	if len(stage) == 1 {
		for i, row := range old {
			if row.row == stage[0].row {
				next[i] = stage[0]
				return next
			}
		}
		return append(next, stage[0])
	}
	slots := make(map[ids.RowID]int, len(old))
	for i, row := range old {
		slots[row.row] = i
	}
	for _, row := range stage {
		if i, ok := slots[row.row]; ok {
			next[i] = row
		} else {
			slots[row.row] = len(next)
			next = append(next, row)
		}
	}
	return next
}

// Conflict UPDATE separately proves cardinality through its affected-row set.
func (w *WriteStage) replaceDistinct(ref ids.ObjectRef, old []rowVersion, rows indexWriteBatch) error {
	return w.replaceRows(ref, old, rows, true, true)
}

func (w *WriteStage) replace(ref ids.ObjectRef, old []rowVersion, rows indexWriteBatch) error {
	return w.replaceRows(ref, old, rows, false, false)
}

// Ordinary UPDATE proves each accepted physical target distinct through its
// frozen scan / FROM winner gate, and excludes existing staged RowIDs before
// choosing this append lane. Ordinary target and RI validation still applies.
func (w *WriteStage) appendUpdateReplacement(ref ids.ObjectRef, old []rowVersion, rows indexWriteBatch) error {
	return w.replaceRows(ref, old, rows, false, true)
}

func (w *WriteStage) replaceRows(ref ids.ObjectRef, old []rowVersion, batch indexWriteBatch, conflictDistinct, appendOnly bool) (err error) {
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
		admission := s.foreignActionAdmissions[ref.Address]
		if admission == nil || !admission.writable {
			return errors.New("stuntgres: missing writable internal action admission")
		}
		var small [8]ids.RowID
		if s.op.session.Engine().locks.missingRowGrant(s.ticket, ref, versionRows(small[:0], old)) >= 0 {
			return errors.New("stuntgres: internal action replacement requires its row grant")
		}
	}
	if len(old) != len(rows) {
		return errors.New("stuntgres: invalid replacement shape")
	}
	var smallModes [1]multiMode
	var modes []multiMode
	if batch.actionMode == 0 {
		modes = smallModes[:min(len(old), 1)]
		if len(old) > 1 {
			modes = make([]multiMode, len(old))
		}
	}
	for i, predecessor := range old {
		mode := batch.actionMode
		if mode == 0 {
			changed, failure := s.uniqueKeyChanged(ref, predecessor.values, rows[i])
			if failure != nil {
				return failure
			}
			mode = multiNoKeyAction
			if changed {
				mode = multiUpdateAction
			}
			modes[i] = mode
		}
		if mode != multiNoKeyAction && mode != multiUpdateAction {
			return errors.New("stuntgres: invalid replacement action intent")
		}
		grant := noKeyUpdate
		if mode == multiUpdateAction {
			grant = rowUpdate
		}
		if err := s.acquireUpdateRowMode(ref, predecessor.row, grant); err != nil {
			return err
		}
	}
retry:
	if err := s.validateIndexCandidates(ref, &batch); err != nil {
		return err
	}
	s.owner.mu.Lock()
	e := s.op.session.Engine()
	e.mu.Lock()
	admission := e.privateCatalogLocked(s.owner.catalog).state
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
	if ref.Domain() != e.domain {
		return errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.Epoch() {
		return ErrCatalogReplaced
	}
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	if s.foreignActionAuth != nil || !conflictDistinct && s.mutationStorage(ref) == nil && s.partitionRelations[ref.Address] == nil {
		if err := s.validateForeignActionLocked(e, ref); err != nil {
			return err
		}
	}
	relation, ok := e.privateCatalogLocked(s.owner.catalog).state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || e.privateStorageLocked(s.owner.catalog, ref.Address) == nil || len(old) != len(rows) {
		return errors.New("stuntgres: invalid update stage")
	}
	for i, row := range rows {
		if old[i].row == 0 || old[i].version == 0 || len(row) != len(relation.columns) {
			return errors.New("stuntgres: invalid staged row")
		}
	}
	if conflictDistinct {
		if s.insertConflict == nil || s.insertConflict.update == nil {
			return errors.New("stuntgres: distinct replacement without conflict update")
		}
		for _, predecessor := range old {
			if _, affected := s.insertConflict.affectedRows[predecessor.row]; affected {
				return conflictUpdateCardinalityError()
			}
		}
	}
	if uint64(len(rows)) > ^uint64(0)-e.nextVersion {
		return errors.New("stuntgres: version identity exhausted")
	}
	var versions, distinctRows []rowVersion
	if appendOnly {
		prefix := w.rows[ref.Address]
		distinctRows = slices.Grow(prefix, len(rows))[:len(prefix)+len(rows)]
		versions = distinctRows[len(prefix):]
	} else {
		versions = make([]rowVersion, len(rows))
	}
	for i, row := range rows {
		predecessor := old[i]
		if identity := batch.candidate(i).exclusion; identity != nil {
			reserved := *identity
			reserved.keyChangedAt, reserved.pendingKeyChange = predecessor.keyChangedAt, predecessor.pendingKeyChange
			reserved.indexAdmission = admission
			versions[i] = reserved
			continue
		}
		base := predecessor.base
		if predecessor.born != 0 {
			base = predecessor.version
		}
		e.nextVersion++
		versions[i] = rowVersion{row: predecessor.row, version: ids.VersionID(e.nextVersion), base: base, values: row, keyChangedAt: predecessor.keyChangedAt, pendingKeyChange: predecessor.pendingKeyChange, indexAdmission: admission}
	}
	if batch.states != nil {
		for i := range versions {
			versions[i].indexOutputs = batch.states[i].indexes
		}
	}
	if serializableRelevant(e.serializable.nodes[s.txRef]) {
		for i, row := range versions {
			predecessor := old[i]
			event := [1]serializableWrite{{row: row.row, version: row.version, base: predecessor.version, baseBorn: predecessor.born, creator: e.serializable.creators[predecessor.version]}}
			if err := e.serializableWriteLocked(s, ref, event[:]); err != nil {
				return err
			}
		}
	}
	if len(versions) != 0 {
		if w.rows == nil {
			w.rows = make(map[ids.ObjectAddress][]rowVersion)
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
			s.multiEvents = append(s.multiEvents, multiEvent{tuple: multiTuple{lockResource{ObjectRef: ref, row: old[i].row}, old[i].version}, mode: mode, successor: next.version})
			s.recordForeignActionReplacement(ref, old[i], next)
		}
	}
	return nil
}
