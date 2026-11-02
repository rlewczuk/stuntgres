package dml

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// InsertOwnRow finds the exact private version or its genuinely admitted
// successor through the command's immutable prior-own prefix index.
func InsertOwnRow(h Host, ref ids.ObjectRef, target storage.RowVersion) (storage.RowVersion, bool) {
	own := h.PrivateRows(ref.Address).Own
	if len(own) == 0 {
		return storage.RowVersion{}, false
	}
	c := h.Command().WriteOverlay()
	if c.OwnVersionSlots == nil {
		c.OwnVersionSlots = make(map[ids.ObjectAddress]map[writeVersionIdentity]int)
	}
	slots := c.OwnVersionSlots[ref.Address]
	if slots == nil {
		slots = make(map[writeVersionIdentity]int, len(own))
		for slot, row := range own {
			slots[writeIdentity(row)] = slot
			if row.Base != 0 {
				slots[writeVersionIdentity{row.Base, row.BaseHeaderStamp()}] = slot
			}
			for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
				slots[writeIdentity(prior.Version)] = slot
			}
		}
		c.OwnVersionSlots[ref.Address] = slots
	}
	slot, found := slots[writeIdentity(target)]
	if !found || own[slot].Row != target.Row {
		return storage.RowVersion{}, false
	}
	return own[slot], true
}

func addInsertWriteKey(o *InsertWriteKeyOverlay, h Host, bound *index.UniqueBoundKey, evaluation *exec.Context, row storage.RowVersion, slot int, buckets map[uint64][]int, purpose index.ProbePurpose) error {
	member, keys, err := ExistingUniqueOutput(h, bound, row, evaluation, purpose)
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

func probeInsertWriteOverlay(h Host, ref ids.ObjectRef, bound *index.UniqueBoundKey, evaluation *exec.Context, key []sqltype.Value, excluded []storage.RowVersion, purpose index.ProbePurpose, headers *storage.HeapHeaderRoot) (storage.RowVersion, bool, error) {
	c := h.Command().WriteOverlay()
	if c.Overlays == nil {
		c.Overlays = make(map[*index.UniqueBoundKey]*InsertWriteKeyOverlay)
	}
	o := c.Overlays[bound]
	if o == nil {
		o = &InsertWriteKeyOverlay{Stage: make(map[uint64][]int), Own: make(map[uint64][]int)}
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
			row := headers.Apply(rows[slot])
			if overlayIndex == 1 {
				if _, shadowed := c.AffectedSlot(row); shadowed {
					continue
				}
			}
			private := h.PrivateRows(ref.Address)
			if index.UniqueRowExcluded(row.Row, row.Version, excluded) || index.UniqueRowDeleted(row, private.OwnDeletes, private.StagedDeletes) {
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
