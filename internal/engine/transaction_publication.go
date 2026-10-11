package engine

import (
	"errors"
)

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
	var err error
	writes, deletes, c, err = e.prepareTemporaryCommit(op, ref, writes, deletes, c)
	if err != nil {
		return err
	}
	return e.publishRowsPrepared(op, ref, writes, deletes, c, authority, nil)
}

func (e *Engine) publishRowsPrepared(op *Operation, ref TxRef, writes map[ObjectAddress][]rowVersion, deletes map[ObjectAddress]*privateDeletionRoot, c *catalogChanges, authority *catalogAuthority, phase *preparedPublication) error {
	materialize := phase == nil || !phase.preparing
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
		if err := e.catalogAuthorityPreflightLocked(authority, nil); err != nil {
			return err
		}
		if err := e.preparedPublicationPreflightLocked(op, phase); err != nil {
			return err
		}
		if err := e.discardResetPreflightLocked(op); err != nil {
			return err
		}
		if err := e.serializablePrecommitLocked(ref); err != nil {
			return err
		}
		if phase != nil && phase.preparing {
			e.installPreparedLocked(phase)
			return nil
		}
		e.serializableCommittedLocked(ref, e.commitSeq)
		e.finishPreparedLocked(phase)
		e.finishDiscardRoleResetLocked(op)
		return nil
	}
	_, output := sessionPresentation(op.session)
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
		if len(e.indexRoots) != 0 || c != nil {
			captureCatalog := current
			if nextCatalog != nil {
				captureCatalog = nextCatalog
			}
			indexStores = captureIndexStores(e.storage, captureCatalog)
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
			if materialize {
				nextSequences = make(map[ObjectAddress]*sequenceGenerator, len(e.sequences)+len(c.generators))
				for a, g := range e.sequences {
					nextSequences[a] = g
				}
				nextStorage = make(map[ObjectAddress]*relationStorage, len(e.storage))
				for a, store := range e.storage {
					nextStorage[a] = store
				}
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
		var disclosure AuthorizationView
		if err == nil {
			disclosure, err = e.authorizationLocked(op)
			if nextCatalog != nil {
				disclosure.catalog = CatalogRead{e, nextCatalog}
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if c != nil {
			for a, shape := range c.storage {
				if materialize {
					nextStorage[a] = shape.replacement
				}
			}
			for a, d := range c.descriptors {
				if d == nil && materialize {
					delete(nextSequences, a)
					delete(nextStorage, a)
				}
				if a.ClassID == NamespaceClassID && current.descriptors[a] != d {
					detached := *nextCatalog
					nextCatalog = &detached
				}
			}
			if materialize {
				for a, g := range c.generators {
					if nextCatalog.descriptors[a] != nil {
						nextSequences[a] = g
					}
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
			if err := e.prepareRowPublication(&prepared[i], ref, materialize); err != nil {
				return err
			}
		}
		indexCatalog := nextCatalog
		if indexCatalog == nil {
			indexCatalog = current
		}
		var indexRoots map[ObjectAddress]*logicalIndexRoot
		if len(indexPredecessors) != 0 || c != nil {
			buildStores := indexStores
			var rebuildIndexes map[ObjectAddress]bool
			if c != nil && len(c.storage) > 0 {
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
			var rebuildIntents map[ObjectAddress]*relationDescriptor
			if c != nil {
				rebuildIntents = c.indexRebuilds
			}
			indexRoots, indexErr = e.prepareIndexPublication(op.Context(), current, indexCatalog, buildStores, indexPredecessors, rebuildIndexes, rebuildIntents, prepared, materialize, output, &disclosure)
			if indexErr != nil {
				return indexErr
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
		changed := e.state != current || c != nil && e.commitSeq != frontier || !indexStoresUnchanged(e.storage, indexStores, current, indexCatalog, indexRoots, indexPredecessors)
		if err == nil {
			for _, p := range prepared {
				if e.storage[p.address] != p.original || p.store.generation != p.generation || p.store.ends != p.ends || len(p.store.versions) != len(p.root) {
					changed = true
					break
				}
				current, ok := e.state.descriptors[p.address].(*relationDescriptor)
				if p.originalRelation == nil {
					if e.state.descriptors[p.address] != nil {
						err = ErrCatalogReplaced
						break
					}
				} else if !ok || !sameRelationRowShape(p.originalRelation, current) {
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
			if err == nil {
				err = e.roleRetirementPreflightLocked(c, nextCatalog)
			}
		}
		if err == nil {
			err = e.catalogAuthorityPreflightLocked(authority, c)
		}
		if err == nil {
			err = e.preparedPublicationPreflightLocked(op, phase)
		}
		if err == nil {
			err = e.discardResetPreflightLocked(op)
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
		if c != nil && materialize {
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
		if err := e.serializablePrecommitLocked(ref); err != nil {
			session.mu.Unlock()
			e.mu.Unlock()
			return err
		}
		if phase != nil && phase.preparing {
			e.installPreparedLocked(phase)
			session.mu.Unlock()
			e.mu.Unlock()
			return nil
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
				row.indexAdmission = nil
				if row.pendingKeyChange {
					row.keyChangedAt = row.born
					row.pendingKeyChange = false
				}
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
			for _, shape := range c.storage {
				if shape.truncated && shape.original != nil {
					shape.original.truncateLineage = shape.replacement.truncateLineage
				}
				if shape.rewritten && shape.relation.relationKind == materializedViewRelation {
					for i := range shape.replacement.versions {
						shape.replacement.versions[i].born = e.commitSeq + 1
					}
				}
			}
			e.sequences = nextSequences
			e.storage = nextStorage
			e.state = nextCatalog
			e.nextRevision = nextRevision
			if c.roles != nil {
				e.roles = c.roles
			}
			for a, d := range c.descriptors {
				if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == session.token {
					session.temporaryNamespace = a
				}
			}
		}
		preserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.indexRoots)
		e.indexRoots = indexRoots
		e.commitSeq++
		e.serializableCommittedLocked(ref, e.commitSeq)
		e.finishPreparedLocked(phase)
		e.finishDiscardRoleResetLocked(op)
		session.mu.Unlock()
		e.mu.Unlock()
		return nil
	}
}

// Caller holds Session.mu; row/catalog publication additionally holds Engine.mu.
func publicationOperationLocked(e *Engine, op *Operation) error {
	if op == nil || op.self != op || op.session == nil || op.session.token.owner != e {
		return errors.New("stuntgres: foreign or missing operation")
	}
	if op.done || op.session.state != live {
		if op.session.timeouts.fatal != nil {
			copy := *op.session.timeouts.fatal
			return &copy
		}
		return errors.New("stuntgres: inactive operation")
	}
	if _, ok := op.session.operations[op]; !ok {
		return errors.New("stuntgres: inactive operation")
	}
	return op.fatalErrorLocked(op.ctx.Err())
}
