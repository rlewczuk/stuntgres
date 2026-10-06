package engine

import (
	"errors"
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

func (w *WriteStage) replace(ref ObjectRef, old []rowVersion, rows [][]sqltype.Value) error {
	if w == nil || w.self != w || w.scope == nil {
		return errors.New("stuntgres: foreign or missing write stage")
	}
	s := w.scope
	if err := s.active(); err != nil {
		return err
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	e := s.op.session.Engine()
	e.mu.Lock()
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
	relation, ok := e.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || e.storage[ref.Address] == nil || len(old) != len(rows) {
		return errors.New("stuntgres: invalid update stage")
	}
	for i, row := range rows {
		if old[i].row == 0 || old[i].version == 0 || len(row) != len(relation.columns) {
			return errors.New("stuntgres: invalid staged row")
		}
	}
	if uint64(len(rows)) > ^uint64(0)-e.nextVersion {
		return errors.New("stuntgres: version identity exhausted")
	}
	versions := make([]rowVersion, len(rows))
	for i, row := range rows {
		predecessor := old[i]
		base := predecessor.base
		if predecessor.born != 0 {
			base = predecessor.version
		}
		e.nextVersion++
		versions[i] = rowVersion{row: predecessor.row, version: VersionID(e.nextVersion), base: base, values: row}
	}
	if len(versions) != 0 {
		if w.rows == nil {
			w.rows = make(map[ObjectAddress][]rowVersion)
		}
		w.rows[ref.Address] = mergePrivateRows(w.rows[ref.Address], versions, true)
		w.replacing = true
	}
	return nil
}
