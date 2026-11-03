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
	Address                ids.ObjectAddress
	Store                  *storage.Relation
	Original               *storage.Relation
	OriginalRelation       *catalog.Relation
	Relation               *catalog.Relation
	Root, Next, Writes     []storage.RowVersion
	Ends, NextEnds         *storage.DeletionRoot
	Retained, NextRetained *storage.HeapPredecessor
	Deletion               *visibility.PrivateDeletes
	Generation             uint64
	Headers, NextHeaders   *storage.HeapHeaderRoot
	Ended                  []storage.RowVersion
	FullXID                uint64
	Slots                  []int
	SingleSlot             int
}

// PrepareRowPublication validates p and, when materialize is set, builds its
// next heads, slots and end roots. Validation is shared by prepare and
// commit. Only commit constructs install heads, deletion maps and slot
// manifests; prepare transfers the private roots. It runs outside Engine.mu
// over private roots; domain is the engine's catalog domain, which every
// deletion candidate must carry.
func PrepareRowPublication(p *RowPublication, domain *ids.Domain, ref ids.TxRef, materialize bool) error {
	replacements := 0
	for _, row := range p.Writes {
		if row.Row == 0 || row.Version == 0 || row.Born != 0 || len(row.Values) != len(p.Relation.Columns) {
			return errors.New("stuntgres: invalid private row version")
		}
		if row.Base != 0 {
			replacements++
		}
	}
	find := func(rows []storage.RowVersion, id ids.RowID, version ids.VersionID, hint int) (storage.RowVersion, int, bool) {
		row, slot, ok := storage.RowByVersion(rows, id, version, hint)
		if !ok && p.Headers != nil {
			row, _, ok = storage.RowByVersion(p.Headers.Additional, id, version, -1)
			slot = -1
		}
		return p.Headers.Apply(row), slot, ok
	}
	p.SingleSlot = -1
	if materialize {
		p.NextEnds = p.Ends
	}
	if materialize && replacements != 0 && len(p.Writes) > 1 {
		p.Slots = make([]int, len(p.Writes))
	}
	var slots map[ids.VersionID]int
	if replacements > 1 {
		slots = make(map[ids.VersionID]int, len(p.Root))
		for slot, row := range p.Root {
			slots[row.Version] = slot
		}
	}
	tail := len(p.Root)
	for j, row := range p.Writes {
		slot := tail
		if row.Base != 0 {
			hint := -1
			if slots != nil {
				if value, ok := slots[row.Base]; ok {
					hint = value
				}
			}
			predecessor, baseSlot, found := find(p.Root, row.Row, row.Base, hint)
			// Surgery is irreversible, but does not revoke an already admitted
			// writer. Ignore only slot death here, not MVCC ends or identity.
			if p.Headers.Header(predecessor.Version).Slot == storage.HeapSlotDead {
				predecessor.HeapKilled = false
			}
			if !found || row.BaseHeaderStamp() == predecessor.RepairStamp && !visibility.LiveVersion(predecessor, ids.CommitSeq(^uint64(0)), p.Ends, nil) {
				return errors.New("stuntgres: stale update predecessor")
			}
			// A repair restored this exact predecessor after the private
			// successor was admitted. Keep both physical versions.
			if baseSlot >= 0 && row.BaseHeaderStamp() == predecessor.RepairStamp {
				slot = baseSlot
				if materialize {
					if _, ended := p.Ends.Lookup(predecessor.Row, predecessor.Version); ended {
						if p.NextEnds == p.Ends {
							p.NextEnds = p.Ends.Clone()
						}
						p.NextEnds.Delete(predecessor.Row, predecessor.Version)
					}
				}
			}
		}
		if slot == tail {
			tail++
		}
		if materialize && replacements != 0 {
			if p.Slots != nil {
				p.Slots[j] = slot
			} else {
				p.SingleSlot = slot
			}
		}
	}
	if materialize {
		if replacements == 0 && tail <= cap(p.Root) {
			p.Next = p.Root[:tail]
		} else {
			capacity := tail
			if replacements == 0 {
				capacity = max(tail, cap(p.Root)*2)
			}
			p.Next = make([]storage.RowVersion, tail, capacity)
			copy(p.Next, p.Root)
		}
	}
	p.NextRetained = p.Retained
	for _, row := range p.Writes {
		successor := row.Version
		for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
			version := prior.Version
			if version.Row != row.Row || version.Version == 0 || version.Version >= successor || prior.End != 0 || prior.EndFullXID != 0 || version.PrivateHistory != nil || len(version.Values) != len(p.Relation.Columns) {
				return errors.New("stuntgres: invalid retained heap predecessor")
			}
			successor = version.Version
			if materialize {
				p.Ended = append(p.Ended, version)
				// Additional already owns this repaired payload. Do not
				// fabricate a second ancestry node or change its creator.
				if p.Headers.Header(version.Version).Repaired {
					continue
				}
				version.IndexAdmission, version.IndexOutputs = nil, nil
				p.NextRetained = &storage.HeapPredecessor{Version: version, Next: p.NextRetained}
			}
		}
	}
	if p.Deletion != nil {
		if materialize {
			if p.NextEnds == p.Ends {
				p.NextEnds = p.Ends.Clone()
			}
			if p.NextEnds == nil {
				p.NextEnds = &storage.DeletionRoot{Rows: make(map[ids.RowID]storage.RowEnd, p.Deletion.Len())}
			}
		}
		for id, candidate := range p.Deletion.All() {
			if candidate.Ref.Domain() != domain || candidate.Ref.Epoch != ref.Epoch() || candidate.Ref.Address != p.Address || id == 0 || id != candidate.Row || candidate.Version == 0 || candidate.Storage != p.Store {
				return ids.ErrCatalogReplaced
			}
			predecessors := p.Root
			if candidate.Own {
				predecessors = p.Writes
			}
			predecessor, _, found := find(predecessors, id, candidate.Version, candidate.Ordinal)
			if p.Headers.Header(predecessor.Version).Slot == storage.HeapSlotDead {
				predecessor.HeapKilled = false
			}
			if !found || candidate.Own && predecessor.Base != candidate.Base || len(predecessor.Values) != len(p.Relation.Columns) {
				return errors.New("stuntgres: invalid deletion predecessor")
			}
			if !candidate.Own && candidate.RepairStamp == predecessor.RepairStamp && !visibility.LiveVersion(predecessor, ids.CommitSeq(^uint64(0)), p.Ends, nil) {
				return errors.New("stuntgres: deletion predecessor already ended")
			}
			if materialize {
				p.Ended = append(p.Ended, storage.RowVersion{Row: id, Version: candidate.Version, RepairStamp: candidate.RepairStamp})
				if candidate.RepairStamp == predecessor.RepairStamp {
					p.NextEnds.Set(id, storage.RowEnd{Row: id, Version: candidate.Version, Moved: candidate.Moved})
				}
			}
		}
	}
	if materialize {
		p.NextHeaders = storage.EndHeapHeaders(p.Headers, p.Ended, p.FullXID)
		if p.NextEnds.Len() == 0 {
			p.NextEnds = nil
		}
	}
	return nil
}
