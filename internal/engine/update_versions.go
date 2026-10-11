package engine

import (
	"errors"
	"slices"
	"stuntgres/internal/sqltype"
)

// A captured physical position is only a hint; arbitration is by stable RowID.
func rowByID(rows []rowVersion, id RowID, hint int) (rowVersion, int, bool) {
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
	slots := make(map[RowID]int, len(old))
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

// Only conflict UPDATE's affected-RowID admission proves distinct append-only
// effects within this command. Ordinary UPDATE/MERGE keep replacement merging.
func (w *WriteStage) replaceDistinct(ref ObjectRef, old []rowVersion, rows [][]sqltype.Value) error {
	return w.replaceRows(ref, old, rows, true)
}

func (w *WriteStage) replace(ref ObjectRef, old []rowVersion, rows [][]sqltype.Value) error {
	return w.replaceRows(ref, old, rows, false)
}

func (w *WriteStage) replaceRows(ref ObjectRef, old []rowVersion, rows [][]sqltype.Value, distinct bool) error {
	if w == nil || w.self != w || w.scope == nil {
		return errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	if err := s.active(); err != nil {
		return err
	}
retry:
	s.owner.mu.Lock()
	e := s.op.session.Engine()
	e.mu.Lock()
	admission := e.privateCatalogLocked(s.owner.catalog).state
	if binding := s.uniqueBinding; binding != nil && binding.catalog != admission {
		e.mu.Unlock()
		s.owner.mu.Unlock()
		for i, row := range rows {
			if err := s.checkUniqueRow(ref, row, old[:i+1], rows[:i]); err != nil {
				return err
			}
		}
		goto retry
	}
	defer s.owner.mu.Unlock()
	defer e.mu.Unlock()
	if !s.worked || s.owner.state == TxIdle || s.owner.state == TxAborted {
		return errors.New("stuntgres: inactive write stage")
	}
	if ref.owner != e.owner {
		return errors.New("stuntgres: foreign catalog reference")
	}
	if ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.epoch {
		return ErrCatalogReplaced
	}
	if err := s.validateLockedRelation(e); err != nil {
		return err
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
	if distinct {
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
	if distinct {
		prefix := w.rows[ref.Address]
		distinctRows = slices.Grow(prefix, len(rows))[:len(prefix)+len(rows)]
		versions = distinctRows[len(prefix):]
	} else {
		versions = make([]rowVersion, len(rows))
	}
	for i, row := range rows {
		predecessor := old[i]
		if reserved, ok := s.reservedExclusionRow(ref, row); ok {
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
		versions[i] = rowVersion{row: predecessor.row, version: VersionID(e.nextVersion), base: base, values: row, keyChangedAt: predecessor.keyChangedAt, pendingKeyChange: predecessor.pendingKeyChange, indexAdmission: admission}
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
			w.rows = make(map[ObjectAddress][]rowVersion)
		}
		if distinct {
			w.rows[ref.Address] = distinctRows
		} else {
			w.rows[ref.Address] = mergePrivateRows(w.rows[ref.Address], versions, true)
		}
		w.replacing = true
	}
	return nil
}
