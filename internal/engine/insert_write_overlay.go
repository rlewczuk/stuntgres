package engine

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) writeOverlay() *dml.InsertWriteOverlay {
	if s.insertConflict != nil && s.insertConflict.Update != nil {
		return &s.insertConflict.InsertWriteOverlay
	}
	if s.insertReturning != nil {
		return &s.insertReturning.Overlay
	}
	return nil
}

func (s *StatementScope) insertOwnRow(ref ids.ObjectRef, id ids.RowID) (storage.RowVersion, bool) {
	if len(s.ownWrites[ref.Address]) == 0 {
		return storage.RowVersion{}, false
	}
	c := s.writeOverlay()
	if c.OwnRowSlots == nil {
		c.OwnRowSlots = make(map[ids.ObjectAddress]map[ids.RowID]int)
	}
	slots := c.OwnRowSlots[ref.Address]
	if slots == nil {
		slots = make(map[ids.RowID]int, len(s.ownWrites[ref.Address]))
		for slot, row := range s.ownWrites[ref.Address] {
			slots[row.Row] = slot
		}
		c.OwnRowSlots[ref.Address] = slots
	}
	slot, found := slots[id]
	if !found {
		return storage.RowVersion{}, false
	}
	return s.ownWrites[ref.Address][slot], true
}

func addInsertWriteKey(o *dml.InsertWriteKeyOverlay, s *StatementScope, bound *index.UniqueBoundKey, evaluation *exec.Context, row storage.RowVersion, slot int, buckets map[uint64][]int, purpose index.ProbePurpose) error {
	member, keys, err := s.existingUniqueOutput(bound, row, evaluation, purpose)
	if err != nil || !member {
		return err
	}
	if index.UniqueHasNull(keys) && !bound.Index.SQLIndex.NullsNotDistinct {
		return nil
	}
	hash, err := index.UniqueKeyHash(evaluation.Context, bound.Ordering, bound.Grouping, keys)
	if err != nil {
		return err
	}
	buckets[hash] = append(buckets[hash], slot)
	return nil
}

func (s *StatementScope) probeInsertWriteOverlay(ref ids.ObjectRef, bound *index.UniqueBoundKey, evaluation *exec.Context, key []sqltype.Value, excluded []storage.RowVersion, purpose index.ProbePurpose) (storage.RowVersion, bool, error) {
	c := s.writeOverlay()
	if c.Overlays == nil {
		c.Overlays = make(map[*index.UniqueBoundKey]*dml.InsertWriteKeyOverlay)
	}
	o := c.Overlays[bound]
	if o == nil {
		o = &dml.InsertWriteKeyOverlay{Stage: make(map[uint64][]int), Own: make(map[uint64][]int)}
		c.Overlays[bound] = o
	}
	if !o.OwnReady {
		for slot, row := range s.ownWrites[ref.Address] {
			if err := addInsertWriteKey(o, s, bound, evaluation, row, slot, o.Own, purpose); err != nil {
				return storage.RowVersion{}, false, err
			}
		}
		o.OwnReady = true
	}
	stage := s.stage.rows[ref.Address]
	for o.StageCount < len(stage) {
		if err := addInsertWriteKey(o, s, bound, evaluation, stage[o.StageCount], o.StageCount, o.Stage, purpose); err != nil {
			return storage.RowVersion{}, false, err
		}
		o.StageCount++
	}
	hash, err := index.UniqueKeyHash(evaluation.Context, bound.Ordering, bound.Grouping, key)
	if err != nil {
		return storage.RowVersion{}, false, err
	}
	for overlayIndex, slots := range [2][]int{o.Stage[hash], o.Own[hash]} {
		rows := stage
		if overlayIndex == 1 {
			rows = s.ownWrites[ref.Address]
		}
		for _, slot := range slots {
			row := rows[slot]
			if overlayIndex == 1 {
				if _, shadowed := c.AffectedRows[row.Row]; shadowed {
					continue
				}
			}
			if index.UniqueRowExcluded(row.Row, excluded) || index.UniqueRowDeleted(row.Row, s.ownDeletes[ref.Address]) || index.UniqueRowDeleted(row.Row, s.stage.deletes[ref.Address]) {
				continue
			}
			matches, err := s.matchesExistingUnique(bound, row, evaluation, key, purpose)
			if err != nil {
				return storage.RowVersion{}, false, err
			}
			if matches {
				return row, true, nil
			}
		}
	}
	return storage.RowVersion{}, false, nil
}
