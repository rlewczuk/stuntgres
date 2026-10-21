package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Validation is shared by prepare and commit. Only commit constructs install
// heads, deletion maps and slot manifests; prepare transfers the private roots.
func (e *Engine) prepareRowPublication(p *rowPublication, ref ids.TxRef, materialize bool) error {
	inserts, replacements := 0, 0
	for _, row := range p.writes {
		if row.Row == 0 || row.Version == 0 || row.Born != 0 || len(row.Values) != len(p.relation.Columns) {
			return errors.New("stuntgres: invalid private row version")
		}
		if row.Base == 0 {
			inserts++
		} else {
			replacements++
		}
	}
	if materialize {
		size := len(p.root) + inserts
		if replacements == 0 && size <= cap(p.root) {
			p.next = p.root[:size]
		} else {
			capacity := max(size, cap(p.root)*2)
			if replacements != 0 {
				capacity = size
			}
			p.next = make([]storage.RowVersion, size, capacity)
			copy(p.next, p.root)
		}
	}
	if replacements != 0 {
		var index map[ids.RowID]int
		if replacements > 1 {
			index = make(map[ids.RowID]int, len(p.root))
			for slot, row := range p.root {
				index[row.Row] = slot
			}
		}
		if materialize && len(p.writes) > 1 {
			p.slots = make([]int, len(p.writes))
		}
		tail := len(p.root)
		for j, row := range p.writes {
			if row.Base == 0 {
				if materialize {
					p.slots[j] = tail
				}
				tail++
				continue
			}
			var predecessor storage.RowVersion
			var slot int
			var found bool
			if index != nil {
				slot, found = index[row.Row]
				if found {
					predecessor = p.root[slot]
				}
			} else {
				predecessor, slot, found = storage.RowByID(p.root, row.Row, -1)
			}
			if !found || predecessor.Version != row.Base || !visibility.LiveVersion(predecessor, ids.CommitSeq(^uint64(0)), p.ends, nil) {
				return errors.New("stuntgres: stale update predecessor")
			}
			if materialize {
				if p.slots == nil {
					p.singleSlot = slot
				} else {
					p.slots[j] = slot
				}
			}
		}
	}
	if p.deletion != nil {
		if materialize {
			count := len(p.deletion.Rows)
			if p.ends != nil {
				count += len(p.ends.Rows)
			}
			p.nextEnds = &storage.DeletionRoot{Rows: make(map[ids.RowID]storage.RowEnd, count)}
			if p.ends != nil {
				for id, end := range p.ends.Rows {
					p.nextEnds.Rows[id] = end
				}
			}
		}
		for id, candidate := range p.deletion.Rows {
			if candidate.Ref.Domain() != e.catalog.Domain || candidate.Ref.Epoch != ref.Epoch() || candidate.Ref.Address != p.address || id == 0 || id != candidate.Row || candidate.Version == 0 || candidate.Storage != p.store {
				return ids.ErrCatalogReplaced
			}
			predecessors := p.root
			if candidate.Own {
				predecessors = p.writes
			}
			predecessor, _, found := storage.RowByID(predecessors, id, candidate.Ordinal)
			if !found || predecessor.Version != candidate.Version || candidate.Own && predecessor.Base != candidate.Base || len(predecessor.Values) != len(p.relation.Columns) {
				return errors.New("stuntgres: invalid deletion predecessor")
			}
			if !candidate.Own && !visibility.LiveVersion(predecessor, ids.CommitSeq(^uint64(0)), p.ends, nil) {
				return errors.New("stuntgres: deletion predecessor already ended")
			}
			if materialize {
				p.nextEnds.Rows[id] = storage.RowEnd{Version: candidate.Version}
			}
		}
	}
	return nil
}
