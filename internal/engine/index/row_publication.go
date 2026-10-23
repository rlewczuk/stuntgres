package index

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Prepare immutable replacement heads/end roots outside final locks. Appends
// retain geometric capacity; final installation follows one all-relations veto.
type RowPublication struct {
	Address            ids.ObjectAddress
	Store              *storage.Relation
	Original           *storage.Relation
	OriginalRelation   *catalog.Relation
	Relation           *catalog.Relation
	Root, Next, Writes []storage.RowVersion
	Ends, NextEnds     *storage.DeletionRoot
	Deletion           *visibility.PrivateDeletes
	Generation         uint64
	Slots              []int
	SingleSlot         int
}

// PrepareRowPublication validates p and, when materialize is set, builds its
// next heads, slots and end roots. Validation is shared by prepare and
// commit. Only commit constructs install heads, deletion maps and slot
// manifests; prepare transfers the private roots. It runs outside Engine.mu
// over private roots; domain is the engine's catalog domain, which every
// deletion candidate must carry.
func PrepareRowPublication(p *RowPublication, domain *ids.Domain, ref ids.TxRef, materialize bool) error {
	inserts, replacements := 0, 0
	for _, row := range p.Writes {
		if row.Row == 0 || row.Version == 0 || row.Born != 0 || len(row.Values) != len(p.Relation.Columns) {
			return errors.New("stuntgres: invalid private row version")
		}
		if row.Base == 0 {
			inserts++
		} else {
			replacements++
		}
	}
	if materialize {
		size := len(p.Root) + inserts
		if replacements == 0 && size <= cap(p.Root) {
			p.Next = p.Root[:size]
		} else {
			capacity := max(size, cap(p.Root)*2)
			if replacements != 0 {
				capacity = size
			}
			p.Next = make([]storage.RowVersion, size, capacity)
			copy(p.Next, p.Root)
		}
	}
	if replacements != 0 {
		var index map[ids.RowID]int
		if replacements > 1 {
			index = make(map[ids.RowID]int, len(p.Root))
			for slot, row := range p.Root {
				index[row.Row] = slot
			}
		}
		if materialize && len(p.Writes) > 1 {
			p.Slots = make([]int, len(p.Writes))
		}
		tail := len(p.Root)
		for j, row := range p.Writes {
			if row.Base == 0 {
				if materialize {
					p.Slots[j] = tail
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
					predecessor = p.Root[slot]
				}
			} else {
				predecessor, slot, found = storage.RowByID(p.Root, row.Row, -1)
			}
			if !found || predecessor.Version != row.Base || !visibility.LiveVersion(predecessor, ids.CommitSeq(^uint64(0)), p.Ends, nil) {
				return errors.New("stuntgres: stale update predecessor")
			}
			if materialize {
				if p.Slots == nil {
					p.SingleSlot = slot
				} else {
					p.Slots[j] = slot
				}
			}
		}
	}
	if p.Deletion != nil {
		if materialize {
			count := len(p.Deletion.Rows)
			if p.Ends != nil {
				count += len(p.Ends.Rows)
			}
			p.NextEnds = &storage.DeletionRoot{Rows: make(map[ids.RowID]storage.RowEnd, count)}
			if p.Ends != nil {
				for id, end := range p.Ends.Rows {
					p.NextEnds.Rows[id] = end
				}
			}
		}
		for id, candidate := range p.Deletion.Rows {
			if candidate.Ref.Domain() != domain || candidate.Ref.Epoch != ref.Epoch() || candidate.Ref.Address != p.Address || id == 0 || id != candidate.Row || candidate.Version == 0 || candidate.Storage != p.Store {
				return ids.ErrCatalogReplaced
			}
			predecessors := p.Root
			if candidate.Own {
				predecessors = p.Writes
			}
			predecessor, _, found := storage.RowByID(predecessors, id, candidate.Ordinal)
			if !found || predecessor.Version != candidate.Version || candidate.Own && predecessor.Base != candidate.Base || len(predecessor.Values) != len(p.Relation.Columns) {
				return errors.New("stuntgres: invalid deletion predecessor")
			}
			if !candidate.Own && !visibility.LiveVersion(predecessor, ids.CommitSeq(^uint64(0)), p.Ends, nil) {
				return errors.New("stuntgres: deletion predecessor already ended")
			}
			if materialize {
				p.NextEnds.Rows[id] = storage.RowEnd{Version: candidate.Version}
			}
		}
	}
	return nil
}
