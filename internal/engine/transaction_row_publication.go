package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
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
			p.next = make([]rowVersion, size, capacity)
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
			var predecessor rowVersion
			var slot int
			var found bool
			if index != nil {
				slot, found = index[row.Row]
				if found {
					predecessor = p.root[slot]
				}
			} else {
				predecessor, slot, found = rowByID(p.root, row.Row, -1)
			}
			if !found || predecessor.Version != row.Base || !liveVersion(predecessor, ids.CommitSeq(^uint64(0)), p.ends, nil) {
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
			count := len(p.deletion.rows)
			if p.ends != nil {
				count += len(p.ends.Rows)
			}
			p.nextEnds = &deletionRoot{Rows: make(map[ids.RowID]rowEnd, count)}
			if p.ends != nil {
				for id, end := range p.ends.Rows {
					p.nextEnds.Rows[id] = end
				}
			}
		}
		for id, candidate := range p.deletion.rows {
			if candidate.ref.Domain() != e.catalog.Domain || candidate.ref.Epoch != ref.Epoch() || candidate.ref.Address != p.address || id == 0 || id != candidate.row || candidate.version == 0 || candidate.storage != p.store {
				return ids.ErrCatalogReplaced
			}
			predecessors := p.root
			if candidate.own {
				predecessors = p.writes
			}
			predecessor, _, found := rowByID(predecessors, id, candidate.ordinal)
			if !found || predecessor.Version != candidate.version || candidate.own && predecessor.Base != candidate.base || len(predecessor.Values) != len(p.relation.Columns) {
				return errors.New("stuntgres: invalid deletion predecessor")
			}
			if !candidate.own && !liveVersion(predecessor, ids.CommitSeq(^uint64(0)), p.ends, nil) {
				return errors.New("stuntgres: deletion predecessor already ended")
			}
			if materialize {
				p.nextEnds.Rows[id] = rowEnd{Version: candidate.version}
			}
		}
	}
	return nil
}
