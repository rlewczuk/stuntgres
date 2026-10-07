package engine

import "errors"

// Prepare immutable replacement heads/end roots outside final locks. Appends
// retain geometric capacity; final installation follows one all-relations veto.
type rowPublication struct {
	address            ObjectAddress
	store              *relationStorage
	relation           *relationDescriptor
	root, next, writes []rowVersion
	ends, nextEnds     *deletionRoot
	deletion           *privateDeletionRoot
	generation         uint64
	slots              []int
	singleSlot         int
}

func (e *Engine) publishWrites(op *Operation, ref TxRef, writes map[ObjectAddress][]rowVersion, deletes map[ObjectAddress]*privateDeletionRoot) error {
	if op == nil || op.self != op || op.session == nil || op.session.transactions == nil || ref.attachment == nil || ref.owner != e || ref.session != op.session.token || ref.attachment != op.session.transactions || ref.id == 0 || ref != ref.attachment.ref {
		return errors.New("stuntgres: foreign or stale publication transaction")
	}
	if ref.attachment.state != TxActive && ref.attachment.state != TxImplicit {
		return errors.New("stuntgres: inactive publication transaction")
	}
	if len(writes) == 0 && len(deletes) == 0 {
		e.mu.Lock()
		defer e.mu.Unlock()
		op.session.mu.Lock()
		defer op.session.mu.Unlock()
		if err := publicationOperationLocked(e, op); err != nil {
			return err
		}
		if ref.epoch != e.state.epoch {
			return ErrCatalogReplaced
		}
		return nil
	}
	for {
		prepared := make([]rowPublication, 0, len(writes)+len(deletes))
		e.mu.Lock()
		if ref.epoch != e.state.epoch {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		capture := func(a ObjectAddress, rows []rowVersion, deletion *privateDeletionRoot) error {
			store := e.storage[a]
			relation, live := e.state.descriptors[a].(*relationDescriptor)
			if store == nil || !live || a.SubID != 0 {
				return ErrCatalogReplaced
			}
			prepared = append(prepared, rowPublication{address: a, store: store, relation: relation, root: store.versions, writes: rows, ends: store.ends, deletion: deletion, generation: store.generation, singleSlot: -1})
			return nil
		}
		var err error
		for a, rows := range writes {
			if err = capture(a, rows, deletes[a]); err != nil {
				break
			}
		}
		if err == nil {
			for a, deletion := range deletes {
				if _, written := writes[a]; written {
					continue
				}
				if err = capture(a, nil, deletion); err != nil {
					break
				}
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		for i := range prepared {
			p := &prepared[i]
			inserts, replacements := 0, 0
			for _, row := range p.writes {
				if row.row == 0 || row.version == 0 || row.born != 0 || len(row.values) != len(p.relation.columns) {
					return errors.New("stuntgres: invalid private row version")
				}
				if row.base == 0 {
					inserts++
				} else {
					replacements++
				}
			}
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
			if replacements != 0 {
				var index map[RowID]int
				if replacements > 1 {
					index = make(map[RowID]int, len(p.root))
					for slot, row := range p.root {
						index[row.row] = slot
					}
				}
				if len(p.writes) > 1 {
					p.slots = make([]int, len(p.writes))
				}
				tail := len(p.root)
				for j, row := range p.writes {
					if row.base == 0 {
						p.slots[j] = tail
						tail++
						continue
					}
					var predecessor rowVersion
					var slot int
					var found bool
					if index != nil {
						slot, found = index[row.row]
						if found {
							predecessor = p.root[slot]
						}
					} else {
						predecessor, slot, found = rowByID(p.root, row.row, -1)
					}
					if !found || predecessor.version != row.base || !liveVersion(predecessor, CommitSeq(^uint64(0)), p.ends, nil) {
						return errors.New("stuntgres: stale update predecessor")
					}
					if p.slots == nil {
						p.singleSlot = slot
					} else {
						p.slots[j] = slot
					}
				}
			}
			if p.deletion != nil {
				count := len(p.deletion.rows)
				if p.ends != nil {
					count += len(p.ends.rows)
				}
				p.nextEnds = &deletionRoot{rows: make(map[RowID]rowEnd, count)}
				if p.ends != nil {
					for id, end := range p.ends.rows {
						p.nextEnds.rows[id] = end
					}
				}
				for id, candidate := range p.deletion.rows {
					if candidate.ref.owner != e.owner || candidate.ref.Epoch != ref.epoch || candidate.ref.Address != p.address || id == 0 || id != candidate.row || candidate.version == 0 || candidate.storage != p.store {
						return ErrCatalogReplaced
					}
					predecessors := p.root
					if candidate.own {
						predecessors = p.writes
					}
					predecessor, _, found := rowByID(predecessors, id, candidate.ordinal)
					if !found || predecessor.version != candidate.version || len(predecessor.values) != len(p.relation.columns) {
						return errors.New("stuntgres: invalid deletion predecessor")
					}
					if !candidate.own && !liveVersion(predecessor, CommitSeq(^uint64(0)), p.ends, nil) {
						return errors.New("stuntgres: deletion predecessor already ended")
					}
					p.nextEnds.rows[id] = rowEnd{version: candidate.version}
				}
			}
		}
		session := op.session
		e.mu.Lock()
		session.mu.Lock()
		err = publicationOperationLocked(e, op)
		if err == nil && ref.epoch != e.state.epoch {
			err = ErrCatalogReplaced
		}
		if err == nil && e.commitSeq == CommitSeq(^uint64(0)) {
			err = errors.New("stuntgres: commit sequence exhausted")
		}
		changed := false
		if err == nil {
			for _, p := range prepared {
				if e.storage[p.address] != p.store || p.store.generation != p.generation || p.store.ends != p.ends || len(p.store.versions) != len(p.root) {
					changed = true
					break
				}
				current, ok := e.state.descriptors[p.address].(*relationDescriptor)
				if !ok || !sameRelationRowShape(p.relation, current) {
					err = ErrCatalogReplaced
					break
				}
				if p.store.generation == ^uint64(0) {
					err = errors.New("stuntgres: storage generation exhausted")
					break
				}
			}
		}
		if err != nil {
			session.mu.Unlock()
			e.mu.Unlock()
			return err
		}
		if changed {
			session.mu.Unlock()
			e.mu.Unlock()
			continue
		}
		// No callback, allocation or failure remains after this point.
		for _, p := range prepared {
			for i, row := range p.writes {
				slot := len(p.root) + i
				if p.slots != nil {
					slot = p.slots[i]
				}
				if p.singleSlot >= 0 {
					slot = p.singleSlot
				}
				row.born, row.base = e.commitSeq+1, 0
				p.next[slot] = row
			}
			if p.nextEnds != nil {
				for id, candidate := range p.deletion.rows {
					p.nextEnds.rows[id] = rowEnd{version: candidate.version, end: e.commitSeq + 1}
				}
				p.store.ends = p.nextEnds
			}
			p.store.versions = p.next
			p.store.generation++
		}
		e.commitSeq++
		session.mu.Unlock()
		e.mu.Unlock()
		return nil
	}
}
