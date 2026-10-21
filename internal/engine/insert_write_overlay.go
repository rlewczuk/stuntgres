package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
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
	affectedRows map[ids.RowID]int
	ownRowSlots  map[ids.ObjectAddress]map[ids.RowID]int
	overlays     map[*index.UniqueBoundKey]*insertWriteKeyOverlay
}

func (s *StatementScope) writeOverlay() *insertWriteOverlay {
	if s.insertConflict != nil && s.insertConflict.Update != nil {
		return &s.insertConflict.insertWriteOverlay
	}
	if s.insertReturning != nil {
		return &s.insertReturning.overlay
	}
	return nil
}

func (s *StatementScope) insertOwnRow(ref ids.ObjectRef, id ids.RowID) (storage.RowVersion, bool) {
	if len(s.ownWrites[ref.Address]) == 0 {
		return storage.RowVersion{}, false
	}
	c := s.writeOverlay()
	if c.ownRowSlots == nil {
		c.ownRowSlots = make(map[ids.ObjectAddress]map[ids.RowID]int)
	}
	slots := c.ownRowSlots[ref.Address]
	if slots == nil {
		slots = make(map[ids.RowID]int, len(s.ownWrites[ref.Address]))
		for slot, row := range s.ownWrites[ref.Address] {
			slots[row.Row] = slot
		}
		c.ownRowSlots[ref.Address] = slots
	}
	slot, found := slots[id]
	if !found {
		return storage.RowVersion{}, false
	}
	return s.ownWrites[ref.Address][slot], true
}

func addInsertWriteKey(o *insertWriteKeyOverlay, s *StatementScope, bound *index.UniqueBoundKey, evaluation *exec.Context, row storage.RowVersion, slot int, buckets map[uint64][]int, purpose index.ProbePurpose) error {
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
	if c.overlays == nil {
		c.overlays = make(map[*index.UniqueBoundKey]*insertWriteKeyOverlay)
	}
	o := c.overlays[bound]
	if o == nil {
		o = &insertWriteKeyOverlay{stage: make(map[uint64][]int), own: make(map[uint64][]int)}
		c.overlays[bound] = o
	}
	if !o.ownReady {
		for slot, row := range s.ownWrites[ref.Address] {
			if err := addInsertWriteKey(o, s, bound, evaluation, row, slot, o.own, purpose); err != nil {
				return storage.RowVersion{}, false, err
			}
		}
		o.ownReady = true
	}
	stage := s.stage.rows[ref.Address]
	for o.stageCount < len(stage) {
		if err := addInsertWriteKey(o, s, bound, evaluation, stage[o.stageCount], o.stageCount, o.stage, purpose); err != nil {
			return storage.RowVersion{}, false, err
		}
		o.stageCount++
	}
	hash, err := index.UniqueKeyHash(evaluation.Context, bound.Ordering, bound.Grouping, key)
	if err != nil {
		return storage.RowVersion{}, false, err
	}
	for overlayIndex, slots := range [2][]int{o.stage[hash], o.own[hash]} {
		rows := stage
		if overlayIndex == 1 {
			rows = s.ownWrites[ref.Address]
		}
		for _, slot := range slots {
			row := rows[slot]
			if overlayIndex == 1 {
				if _, shadowed := c.affectedRows[row.Row]; shadowed {
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
