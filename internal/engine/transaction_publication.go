package engine

import (
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// Prepare immutable replacement heads/end roots outside final locks. Appends
// retain geometric capacity; final installation follows one all-relations veto.
type rowPublication struct {
	address            ids.ObjectAddress
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

func (e *Engine) publishWrites(op *Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]rowVersion, deletes map[ids.ObjectAddress]*privateDeletionRoot) error {
	owner := txOwnerOf(ref)
	if op == nil || op.self != op || op.session == nil || op.session.transactions == nil || owner == nil || ref.Domain() != domainOf(e) || ref.Session() != op.session.token || owner != op.session.transactions || ref.ID() == 0 || ref != owner.ref {
		return errors.New("stuntgres: foreign or stale publication transaction")
	}
	if owner.state != TxActive && owner.state != TxImplicit {
		return errors.New("stuntgres: inactive publication transaction")
	}
	return e.publishRows(op, ref, writes, deletes, owner.catalog, nil, nil)
}

func (e *Engine) publishRows(op *Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]rowVersion, deletes map[ids.ObjectAddress]*privateDeletionRoot, c *catalogChanges, authority *catalogAuthority, scope *StatementScope) error {
	var err error
	writes, deletes, c, err = e.prepareTemporaryCommit(op, ref, writes, deletes, c)
	if err != nil {
		return err
	}
	return e.publishRowsPrepared(op, ref, writes, deletes, c, authority, nil, scope)
}

// A reserved empty container is not a write. PREPARE is the explicit exception:
// even an otherwise empty prepared transaction needs a retained public identity.
func publicationNeedsXID(writes map[ids.ObjectAddress][]rowVersion, deletes map[ids.ObjectAddress]*privateDeletionRoot, c *catalogChanges, phase *preparedPublication) bool {
	if phase != nil && phase.preparing {
		return true
	}
	if c != nil && (len(c.descriptors) != 0 || len(c.names) != 0 || len(c.defaultACLs) != 0 ||
		len(c.generators) != 0 || len(c.storage) != 0 || c.roles != nil ||
		len(c.uniqueRoots) != 0 || len(c.indexCandidates) != 0 || len(c.indexRebuilds) != 0) {
		return true
	}
	for _, rows := range writes {
		if len(rows) != 0 {
			return true
		}
	}
	for _, deletion := range deletes {
		if deletion != nil && len(deletion.rows) != 0 {
			return true
		}
	}
	return false
}

func (e *Engine) publishRowsPrepared(op *Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]rowVersion, deletes map[ids.ObjectAddress]*privateDeletionRoot, c *catalogChanges, authority *catalogAuthority, phase *preparedPublication, scope *StatementScope) error {
	materialize := phase == nil || !phase.preparing
	actions, events, err := notificationEffects(op, ref, phase, scope)
	if err != nil {
		return err
	}
	if len(events) != 0 || publicationNeedsXID(writes, deletes, c, phase) {
		e.mu.Lock()
		err := e.assignTransactionXIDLocked(ref)
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if len(writes) == 0 && len(deletes) == 0 && c == nil {
		for {
			notifications := e.prepareNotificationPublication(op.session, actions, events)
			e.mu.Lock()
			op.session.mu.Lock()
			err := publicationOperationLocked(e, op)
			if err == nil && ref.Epoch() != e.catalog.state.epoch {
				err = ErrCatalogReplaced
			}
			if err == nil {
				err = e.catalogAuthorityPreflightLocked(authority, nil)
			}
			if err == nil {
				err = e.preparedPublicationPreflightLocked(op, phase)
			}
			if err == nil {
				err = e.discardResetPreflightLocked(op)
			}
			if err == nil {
				err = e.serializablePrecommitLocked(ref)
			}
			if err != nil {
				op.session.mu.Unlock()
				e.mu.Unlock()
				return err
			}
			if !e.notificationPublicationUnchangedLocked(notifications) {
				op.session.mu.Unlock()
				e.mu.Unlock()
				continue
			}
			// Every replacement and veto precedes this first installation.
			if phase != nil && phase.preparing {
				e.installPreparedLocked(phase)
			} else {
				e.installNotificationsLocked(notifications)
				e.serializableCommittedLocked(ref, e.commitSeq)
				e.recordCommitTimestampLocked(ref)
				e.finishPreparedLocked(phase)
				e.finishDiscardRoleResetLocked(op)
			}
			op.session.mu.Unlock()
			e.mu.Unlock()
			return nil
		}
	}
	input, output := sessionPresentation(op.session)
	if scope != nil {
		input, output = scope.input, scope.output
	}
	for {
		notifications := e.prepareNotificationPublication(op.session, actions, events)
		var nextCatalog *catalogState
		var err error
		if c != nil {
			nextCatalog, err = e.preparePrivateCatalog(op.Context(), c, ref.Epoch())
			if err != nil {
				return err
			}
		}
		prepared := make([]rowPublication, 0, len(writes)+len(deletes))
		e.mu.Lock()
		if ref.Epoch() != e.catalog.state.epoch {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		current, frontier := e.catalog.state, e.commitSeq
		var indexStores map[ids.ObjectAddress]*relationStorage
		if len(e.indexRoots) != 0 || c != nil {
			captureCatalog := current
			if nextCatalog != nil {
				captureCatalog = nextCatalog
			}
			indexStores = captureIndexStores(e.storage, captureCatalog)
		}
		indexPredecessors := e.indexRoots
		var nextSequences map[ids.ObjectAddress]*sequenceGenerator
		var nextStorage map[ids.ObjectAddress]*relationStorage
		var materializedTokens map[ids.ObjectAddress]*materializedLineage
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
				for address, shape := range c.storage {
					if shape.materializedContinuous && shape.original != nil {
						if materializedTokens == nil {
							materializedTokens = make(map[ids.ObjectAddress]*materializedLineage)
						}
						token := shape.original.materializedLineage
						if token == nil {
							token = &materializedLineage{marker: 1}
						}
						materializedTokens[address] = token
					}
				}
				nextSequences = make(map[ids.ObjectAddress]*sequenceGenerator, len(e.sequences)+len(c.generators))
				for a, g := range e.sequences {
					nextSequences[a] = g
				}
				nextStorage = make(map[ids.ObjectAddress]*relationStorage, len(e.storage))
				for a, store := range e.storage {
					nextStorage[a] = store
				}
			}
		}
		capture := func(a ids.ObjectAddress, rows []rowVersion, deletion *privateDeletionRoot) error {
			original := e.storage[a]
			store := e.privateStorageLocked(c, a)
			originalRelation, live := e.catalog.state.descriptors[a].(*relationDescriptor)
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
				disclosure.catalog = e.catalog.read(nextCatalog)
				disclosure.recaptureCatalog(disclosure.catalog)
				if c != nil && c.roles != nil {
					disclosure.roleRecord = c.roles
					disclosure.recaptureCatalog(disclosure.catalog)
				}
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if scope != nil {
			// This publication attempt captured a new coherent authority.
			// Preserve each actually consumed original actor/recipe, including
			// folded calls and RI-local domain calls whose EvalContext ended.
			if err := scope.readmitPreparedPrograms(disclosure); err != nil {
				return err
			}
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
				if a.ClassID == ids.NamespaceClassID && current.descriptors[a] != d {
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
				if a.ClassID == ids.NamespaceClassID && current.descriptors[a] != d {
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
		var indexRoots map[ids.ObjectAddress]*logicalIndexRoot
		if len(indexPredecessors) != 0 || c != nil {
			buildStores := indexStores
			var rebuildIndexes map[ids.ObjectAddress]bool
			if c != nil && len(c.storage) > 0 {
				buildStores = make(map[ids.ObjectAddress]*relationStorage, len(indexStores))
				for a, store := range indexStores {
					buildStores[a] = store
				}
				rebuildIndexes = make(map[ids.ObjectAddress]bool, len(c.storage))
				for a, shape := range c.storage {
					buildStores[a] = shape.replacement
					rebuildIndexes[a] = true
				}
			}
			var indexErr error
			var rebuildIntents map[ids.ObjectAddress]*relationDescriptor
			var indexCandidates map[ids.ObjectAddress]indexRootCandidate
			if c != nil {
				rebuildIntents = c.indexRebuilds
				indexCandidates = c.indexCandidates
			}
			var runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef
			if c != nil {
				for address, shape := range c.storage {
					if shape.materializedConcurrent {
						if runtimeOwners == nil {
							runtimeOwners = make(map[ids.ObjectAddress]authorization.PrincipalRef)
						}
						runtimeOwners[address] = shape.relation.owner
					}
				}
			}
			indexRoots, indexErr = e.prepareIndexPublication(&EvalContext{Context: op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: input, Output: output}, current, indexCatalog, buildStores, indexPredecessors, rebuildIndexes, rebuildIntents, prepared, materialize, output, &disclosure, runtimeOwners, indexCandidates)
			if indexErr != nil {
				return indexErr
			}
		}
		session := op.session
		e.mu.Lock()
		session.mu.Lock()
		err = publicationOperationLocked(e, op)
		if err == nil && ref.Epoch() != e.catalog.state.epoch {
			err = ErrCatalogReplaced
		}
		if err == nil && e.commitSeq == ids.CommitSeq(^uint64(0)) {
			err = errors.New("stuntgres: commit sequence exhausted")
		}
		changed := !e.notificationPublicationUnchangedLocked(notifications) || e.catalog.state != current || c != nil && e.commitSeq != frontier || !indexStoresUnchanged(e.storage, indexStores, current, indexCatalog, indexRoots, indexPredecessors)
		if err == nil {
			for _, p := range prepared {
				if e.storage[p.address] != p.original || p.store.generation != p.generation || p.store.ends != p.ends || len(p.store.versions) != len(p.root) {
					changed = true
					break
				}
				current, ok := e.catalog.state.descriptors[p.address].(*relationDescriptor)
				if p.originalRelation == nil {
					if e.catalog.state.descriptors[p.address] != nil {
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
		nextRevision := e.catalog.nextRevision
		if c != nil && materialize {
			for _, d := range c.descriptors {
				if d != nil {
					if revision := descriptorRevisionFrontier(d); revision >= nextRevision {
						nextRevision = revision
						if nextRevision != ids.DefinitionRevision(^uint64(0)) {
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
		e.installNotificationsLocked(notifications)
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
				row.indexOutputs = nil
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
			for address, shape := range c.storage {
				if shape.truncated && shape.original != nil {
					shape.original.truncateLineage = shape.replacement.truncateLineage
				}
				if token := materializedTokens[address]; token != nil {
					shape.original.materializedLineage = token
					shape.replacement.materializedLineage = token
				}
				if shape.rewritten && shape.relation.relationKind == materializedViewRelation {
					for i := range shape.replacement.versions {
						shape.replacement.versions[i].born = e.commitSeq + 1
					}
				}
			}
			e.sequences = nextSequences
			e.storage = nextStorage
			e.catalog.state = nextCatalog
			e.catalog.nextRevision = nextRevision
			if c.roles != nil {
				e.roles = c.roles
			}
			for a, d := range c.descriptors {
				if ns, ok := d.(*namespaceDescriptor); ok && !ns.temporaryToast && ns.temporary == session.token {
					session.temporaryNamespace = a
				}
			}
		}
		preserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.indexRoots)
		e.indexRoots = indexRoots
		e.commitSeq++
		e.recordCommitTimestampLocked(ref)
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
	if op == nil || op.self != op || op.session == nil || op.session.token.Domain() != domainOf(e) {
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
