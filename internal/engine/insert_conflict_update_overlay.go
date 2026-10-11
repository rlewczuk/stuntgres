package engine

import "stuntgres/internal/sqltype"

// Slots refer to this command's append-only stage or its captured immutable
// prior-own prefix. Provider hashes only select buckets; matches supplies the
// installed ordering equality, predicate, and expression-key semantics.
type conflictUpdateOverlay struct {
	stage      map[uint64][]int
	own        map[uint64][]int
	stageCount int
	ownReady   bool
}

func (s *StatementScope) conflictUpdateOwnRow(ref ObjectRef, id RowID) (rowVersion, bool) {
	if len(s.ownWrites[ref.Address]) == 0 {
		return rowVersion{}, false
	}
	c := s.insertConflict
	if c.ownRowSlots == nil {
		c.ownRowSlots = make(map[RowID]int, len(s.ownWrites[ref.Address]))
		for slot, row := range s.ownWrites[ref.Address] {
			c.ownRowSlots[row.row] = slot
		}
	}
	slot, found := c.ownRowSlots[id]
	if !found {
		return rowVersion{}, false
	}
	return s.ownWrites[ref.Address][slot], true
}

func (o *conflictUpdateOverlay) add(bound *uniqueBoundKey, evaluation *EvalContext, row rowVersion, slot int, buckets map[uint64][]int) error {
	member, err := indexPredicateMatches(evaluation, bound.index.sqlIndex.predicate, row.values)
	if err != nil || !member {
		return err
	}
	if err := bound.ordering.fillKeys(row.values, bound.scratch, evaluation); err != nil {
		return err
	}
	if uniqueHasNull(bound.scratch) && !bound.index.sqlIndex.nullsNotDistinct {
		return nil
	}
	hash, err := uniqueKeyHash(evaluation.Context, bound.ordering, bound.grouping, bound.scratch)
	if err != nil {
		return err
	}
	buckets[hash] = append(buckets[hash], slot)
	return nil
}

func (s *StatementScope) probeConflictUpdateOverlay(ref ObjectRef, bound *uniqueBoundKey, evaluation *EvalContext, key []sqltype.Value, excluded []rowVersion) (rowVersion, bool, error) {
	c := s.insertConflict
	if c.overlays == nil {
		c.overlays = make(map[*uniqueBoundKey]*conflictUpdateOverlay)
	}
	o := c.overlays[bound]
	if o == nil {
		o = &conflictUpdateOverlay{stage: make(map[uint64][]int), own: make(map[uint64][]int)}
		c.overlays[bound] = o
	}
	if !o.ownReady {
		for slot, row := range s.ownWrites[ref.Address] {
			if err := o.add(bound, evaluation, row, slot, o.own); err != nil {
				return rowVersion{}, false, err
			}
		}
		o.ownReady = true
	}
	stage := s.stage.rows[ref.Address]
	for o.stageCount < len(stage) {
		if err := o.add(bound, evaluation, stage[o.stageCount], o.stageCount, o.stage); err != nil {
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
			matches, err := bound.matches(evaluation, row.values, key)
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
