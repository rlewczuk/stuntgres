package engine

import "errors"

// Prepare immutable replacement heads/end roots outside final locks. Appends
// retain geometric capacity; final installation follows one all-relations veto.
type rowPublication struct {
	address            ObjectAddress
	store              *relationStorage
	original           *relationStorage
	originalRelation   *relationDescriptor
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
	return e.publishRows(op, ref, writes, deletes, ref.attachment.catalog, nil)
}

func (e *Engine) publishRows(op *Operation, ref TxRef, writes map[ObjectAddress][]rowVersion, deletes map[ObjectAddress]*privateDeletionRoot, c *catalogChanges, authority *catalogAuthority) error {
	if len(writes) == 0 && len(deletes) == 0 && c == nil {
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
		if err := e.catalogAuthorityPreflightLocked(authority); err != nil {
			return err
		}
		return nil
	}
	for {
		var nextCatalog *catalogState
		var err error
		if c != nil {
			nextCatalog, err = e.preparePrivateCatalog(op.Context(), c, ref.epoch)
			if err != nil {
				return err
			}
		}
		prepared := make([]rowPublication, 0, len(writes)+len(deletes))
		e.mu.Lock()
		if ref.epoch != e.state.epoch {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		current, frontier := e.state, e.commitSeq
		var indexStores map[ObjectAddress]*relationStorage
		if len(e.indexRoots) != 0 {
			indexStores = captureIndexStores(e.storage, current)
		}
		indexPredecessors := e.indexRoots
		var nextSequences map[ObjectAddress]*sequenceGenerator
		var nextStorage map[ObjectAddress]*relationStorage
		if c != nil {
			if c.cachedBase != current {
				e.mu.Unlock()
				continue
			}
			if err = e.catalogPreflightLocked(c); err != nil {
				e.mu.Unlock()
				return err
			}
			nextSequences = make(map[ObjectAddress]*sequenceGenerator, len(e.sequences)+len(c.generators))
			for a, g := range e.sequences {
				nextSequences[a] = g
			}
			nextStorage = make(map[ObjectAddress]*relationStorage, len(e.storage))
			for a, store := range e.storage {
				nextStorage[a] = store
			}
		}
		capture := func(a ObjectAddress, rows []rowVersion, deletion *privateDeletionRoot) error {
			original := e.storage[a]
			store := e.privateStorageLocked(c, a)
			originalRelation, live := e.state.descriptors[a].(*relationDescriptor)
			relation := originalRelation
			if c != nil {
				relation, live = nextCatalog.descriptors[a].(*relationDescriptor)
			}
			if store == nil || !live || a.SubID != 0 {
				return ErrCatalogReplaced
			}
			prepared = append(prepared, rowPublication{address: a, store: store, original: original, originalRelation: originalRelation, relation: relation, root: store.versions, writes: rows, ends: store.ends, deletion: deletion, generation: store.generation, singleSlot: -1})
			return nil
		}
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
		if c != nil {
			for a, shape := range c.storage {
				nextStorage[a] = shape.replacement
			}
			for a, d := range c.descriptors {
				if d == nil {
					delete(nextSequences, a)
					delete(nextStorage, a)
				}
				if a.ClassID == NamespaceClassID && current.descriptors[a] != d {
					detached := *nextCatalog
					nextCatalog = &detached
				}
			}
			for a, g := range c.generators {
				if nextCatalog.descriptors[a] != nil {
					nextSequences[a] = g
				}
			}
			for a, d := range c.descriptors {
				if a.ClassID == NamespaceClassID && current.descriptors[a] != d {
					if err := nextCatalog.advanceNamespaceGeneration(); err != nil {
						return err
					}
					break
				}
			}
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
		indexCatalog := nextCatalog
		if indexCatalog == nil {
			indexCatalog = current
		}
		var indexRoots map[ObjectAddress]*logicalIndexRoot
		if len(indexPredecessors) != 0 {
			buildStores := indexStores
			var rebuildIndexes map[ObjectAddress]bool
			if len(indexStores) > 0 && c != nil && len(c.storage) > 0 {
				buildStores = make(map[ObjectAddress]*relationStorage, len(indexStores))
				for a, store := range indexStores {
					buildStores[a] = store
				}
				rebuildIndexes = make(map[ObjectAddress]bool, len(c.storage))
				for a, shape := range c.storage {
					buildStores[a] = shape.replacement
					rebuildIndexes[a] = true
				}
			}
			var indexErr error
			indexRoots, indexErr = prepareIndexCatalog(op.Context(), current, indexCatalog, buildStores, indexPredecessors, rebuildIndexes)
			if indexErr != nil {
				return indexErr
			}
			for i := range prepared {
				p := &prepared[i]
				for a, d := range indexCatalog.descriptors {
					index, ok := d.(*relationDescriptor)
					if !ok || index.sqlIndex == nil || index.sqlIndex.parent != p.address {
						continue
					}
					root, err := mergeIndexRoot(op.Context(), p.relation, index.sqlIndex, indexRoots[a], p)
					if err != nil {
						return err
					}
					indexRoots[a] = root
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
		changed := e.state != current || c != nil && e.commitSeq != frontier || !indexStoresUnchanged(e.storage, indexStores, indexCatalog, indexRoots, indexPredecessors)
		if err == nil {
			for _, p := range prepared {
				if e.storage[p.address] != p.original || p.store.generation != p.generation || p.store.ends != p.ends || len(p.store.versions) != len(p.root) {
					changed = true
					break
				}
				current, ok := e.state.descriptors[p.address].(*relationDescriptor)
				if !ok || !sameRelationRowShape(p.originalRelation, current) {
					err = ErrCatalogReplaced
					break
				}
				if p.store.generation == ^uint64(0) {
					err = errors.New("stuntgres: storage generation exhausted")
					break
				}
			}
		}
		if err == nil && !changed && c != nil {
			err = e.catalogPreflightLocked(c)
		}
		if err == nil {
			err = e.catalogAuthorityPreflightLocked(authority)
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
		nextRevision := e.nextRevision
		if c != nil {
			for _, d := range c.descriptors {
				if d != nil {
					if revision := descriptorRevisionFrontier(d); revision >= nextRevision {
						nextRevision = revision
						if nextRevision != DefinitionRevision(^uint64(0)) {
							nextRevision++
						}
					}
				}
			}
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
		if c != nil {
			e.sequences = nextSequences
			e.storage = nextStorage
			e.state = nextCatalog
			e.nextRevision = nextRevision
			for a, d := range c.descriptors {
				if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == session.token {
					session.temporaryNamespace = a
				}
			}
		}
		preserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.indexRoots)
		e.indexRoots = indexRoots
		e.commitSeq++
		session.mu.Unlock()
		e.mu.Unlock()
		return nil
	}
}
