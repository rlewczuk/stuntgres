package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Slots refer to this command's append-only stage or its captured immutable
// prior-own prefix. Provider hashes only select buckets; matches supplies the
// installed ordering equality, predicate, and expression-key semantics.
type insertWriteKeyOverlay struct {
	stage      map[uint64][]int
	own        map[uint64][]int
	stageCount int
	ownReady   bool
}

type insertWriteOverlay struct {
	affectedRows map[RowID]int
	ownRowSlots  map[ids.ObjectAddress]map[RowID]int
	overlays     map[*uniqueBoundKey]*insertWriteKeyOverlay
}

func (s *StatementScope) writeOverlay() *insertWriteOverlay {
	if s.insertConflict != nil && s.insertConflict.update != nil {
		return &s.insertConflict.insertWriteOverlay
	}
	if s.insertReturning != nil {
		return &s.insertReturning.overlay
	}
	return nil
}

func (s *StatementScope) insertOwnRow(ref ids.ObjectRef, id RowID) (rowVersion, bool) {
	if len(s.ownWrites[ref.Address]) == 0 {
		return rowVersion{}, false
	}
	c := s.writeOverlay()
	if c.ownRowSlots == nil {
		c.ownRowSlots = make(map[ids.ObjectAddress]map[RowID]int)
	}
	slots := c.ownRowSlots[ref.Address]
	if slots == nil {
		slots = make(map[RowID]int, len(s.ownWrites[ref.Address]))
		for slot, row := range s.ownWrites[ref.Address] {
			slots[row.row] = slot
		}
		c.ownRowSlots[ref.Address] = slots
	}
	slot, found := slots[id]
	if !found {
		return rowVersion{}, false
	}
	return s.ownWrites[ref.Address][slot], true
}

func (o *insertWriteKeyOverlay) add(s *StatementScope, bound *uniqueBoundKey, evaluation *EvalContext, row rowVersion, slot int, buckets map[uint64][]int, purpose indexProbePurpose) error {
	member, keys, err := s.existingUniqueOutput(bound, row, evaluation, purpose)
	if err != nil || !member {
		return err
	}
	if uniqueHasNull(keys) && !bound.index.sqlIndex.nullsNotDistinct {
		return nil
	}
	hash, err := uniqueKeyHash(evaluation.Context, bound.ordering, bound.grouping, keys)
	if err != nil {
		return err
	}
	buckets[hash] = append(buckets[hash], slot)
	return nil
}

func (s *StatementScope) probeInsertWriteOverlay(ref ids.ObjectRef, bound *uniqueBoundKey, evaluation *EvalContext, key []sqltype.Value, excluded []rowVersion, purpose indexProbePurpose) (rowVersion, bool, error) {
	c := s.writeOverlay()
	if c.overlays == nil {
		c.overlays = make(map[*uniqueBoundKey]*insertWriteKeyOverlay)
	}
	o := c.overlays[bound]
	if o == nil {
		o = &insertWriteKeyOverlay{stage: make(map[uint64][]int), own: make(map[uint64][]int)}
		c.overlays[bound] = o
	}
	if !o.ownReady {
		for slot, row := range s.ownWrites[ref.Address] {
			if err := o.add(s, bound, evaluation, row, slot, o.own, purpose); err != nil {
				return rowVersion{}, false, err
			}
		}
		o.ownReady = true
	}
	stage := s.stage.rows[ref.Address]
	for o.stageCount < len(stage) {
		if err := o.add(s, bound, evaluation, stage[o.stageCount], o.stageCount, o.stage, purpose); err != nil {
			return rowVersion{}, false, err
		}
		o.stageCount++
	}
	hash, err := uniqueKeyHash(evaluation.Context, bound.ordering, bound.grouping, key)
	if err != nil {
		return rowVersion{}, false, err
	}
	for overlayIndex, slots := range [2][]int{o.stage[hash], o.own[hash]} {
		rows := stage
		if overlayIndex == 1 {
			rows = s.ownWrites[ref.Address]
		}
		for _, slot := range slots {
			row := rows[slot]
			if overlayIndex == 1 {
				if _, shadowed := c.affectedRows[row.row]; shadowed {
					continue
				}
			}
			if uniqueRowExcluded(row.row, excluded) || uniqueRowDeleted(row.row, s.ownDeletes[ref.Address]) || uniqueRowDeleted(row.row, s.stage.deletes[ref.Address]) {
				continue
			}
			matches, err := s.matchesExistingUnique(bound, row, evaluation, key, purpose)
			if err != nil {
				return rowVersion{}, false, err
			}
			if matches {
				return row, true, nil
			}
		}
	}
	return rowVersion{}, false, nil
}
