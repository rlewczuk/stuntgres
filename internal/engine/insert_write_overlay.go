package engine

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func insertOwnRow(h dml.Host, ref ids.ObjectRef, id ids.RowID) (storage.RowVersion, bool) {
	own := h.PrivateRows(ref.Address).Own
	if len(own) == 0 {
		return storage.RowVersion{}, false
	}
	c := h.Command().WriteOverlay()
	if c.OwnRowSlots == nil {
		c.OwnRowSlots = make(map[ids.ObjectAddress]map[ids.RowID]int)
	}
	slots := c.OwnRowSlots[ref.Address]
	if slots == nil {
		slots = make(map[ids.RowID]int, len(own))
		for slot, row := range own {
			slots[row.Row] = slot
		}
		c.OwnRowSlots[ref.Address] = slots
	}
	slot, found := slots[id]
	if !found {
		return storage.RowVersion{}, false
	}
	return own[slot], true
}

func addInsertWriteKey(o *dml.InsertWriteKeyOverlay, h dml.Host, bound *index.UniqueBoundKey, evaluation *exec.Context, row storage.RowVersion, slot int, buckets map[uint64][]int, purpose index.ProbePurpose) error {
	member, keys, err := existingUniqueOutput(h, bound, row, evaluation, purpose)
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

func probeInsertWriteOverlay(h dml.Host, ref ids.ObjectRef, bound *index.UniqueBoundKey, evaluation *exec.Context, key []sqltype.Value, excluded []storage.RowVersion, purpose index.ProbePurpose) (storage.RowVersion, bool, error) {
	c := h.Command().WriteOverlay()
	if c.Overlays == nil {
		c.Overlays = make(map[*index.UniqueBoundKey]*dml.InsertWriteKeyOverlay)
	}
	o := c.Overlays[bound]
	if o == nil {
		o = &dml.InsertWriteKeyOverlay{Stage: make(map[uint64][]int), Own: make(map[uint64][]int)}
		c.Overlays[bound] = o
	}
	if !o.OwnReady {
		for slot, row := range h.PrivateRows(ref.Address).Own {
			if err := addInsertWriteKey(o, h, bound, evaluation, row, slot, o.Own, purpose); err != nil {
				return storage.RowVersion{}, false, err
			}
		}
		o.OwnReady = true
	}
	stage := h.PrivateRows(ref.Address).Staged
	for o.StageCount < len(stage) {
		if err := addInsertWriteKey(o, h, bound, evaluation, stage[o.StageCount], o.StageCount, o.Stage, purpose); err != nil {
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
			rows = h.PrivateRows(ref.Address).Own
		}
		for _, slot := range slots {
			row := rows[slot]
			if overlayIndex == 1 {
				if _, shadowed := c.AffectedRows[row.Row]; shadowed {
					continue
				}
			}
			private := h.PrivateRows(ref.Address)
			if index.UniqueRowExcluded(row.Row, excluded) || index.UniqueRowDeleted(row.Row, private.OwnDeletes) || index.UniqueRowDeleted(row.Row, private.StagedDeletes) {
				continue
			}
			matches, err := matchesExistingUnique(h, bound, row, evaluation, key, purpose)
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
