package engine

import (
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Prepare immutable replacement heads/end roots outside final locks. Appends
// retain geometric capacity; final installation follows one all-relations veto.
type rowPublication struct {
	address            ids.ObjectAddress
	store              *storage.Relation
	original           *storage.Relation
	originalRelation   *catalog.Relation
	relation           *catalog.Relation
	root, next, writes []storage.RowVersion
	ends, nextEnds     *storage.DeletionRoot
	deletion           *visibility.PrivateDeletes
	generation         uint64
	slots              []int
	singleSlot         int
}

func (e *Engine) publishWrites(op *Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, scope *StatementScope) error {
	owner := txOwnerOf(ref)
	if op == nil || op.self != op || op.session == nil || op.session.transactions == nil || owner == nil || ref.Domain() != domainOf(e) || ref.Session() != op.session.token || owner != op.session.transactions || ref.ID() == 0 || ref != owner.ref {
		return errors.New("stuntgres: foreign or stale publication transaction")
	}
	if owner.state != TxActive && owner.state != TxImplicit {
		return errors.New("stuntgres: inactive publication transaction")
	}
	return e.publishRows(op, ref, writes, deletes, owner.catalog, nil, scope)
}

func (e *Engine) publishRows(op *Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *catalogChanges, authority *catalogAuthority, scope *StatementScope) error {
	var err error
	writes, deletes, c, err = e.prepareTemporaryCommit(op, ref, writes, deletes, c)
	if err != nil {
		return err
	}
	return e.publishRowsPrepared(op, ref, writes, deletes, c, authority, nil, scope)
}

// A reserved empty container is not a write. PREPARE is the explicit exception:
// even an otherwise empty prepared transaction needs a retained public identity.
func publicationNeedsXID(writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *catalogChanges, phase *preparedPublication) bool {
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
		if deletion != nil && len(deletion.Rows) != 0 {
			return true
		}
	}
	return false
}

func (e *Engine) publishRowsPrepared(op *Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *catalogChanges, authority *catalogAuthority, phase *preparedPublication, scope *StatementScope) error {
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
	remoteSettled := false
	if len(writes) == 0 && len(deletes) == 0 && c == nil {
		for {
			notifications := e.prepareNotificationPublication(op.session, actions, events)
		emptyFence:
			e.mu.Lock()
			op.session.mu.Lock()
			err := publicationOperationLocked(e, op)
			if err == nil && ref.Epoch() != e.catalog.state.Epoch() {
				err = ids.ErrCatalogReplaced
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
				err = e.visibility.SerializablePrecommitLocked(ref)
			}
			if err != nil {
				op.session.mu.Unlock()
				e.mu.Unlock()
				return err
			}
			if !e.notificationPublicationUnchangedLocked(notifications) {
				op.session.mu.Unlock()
				e.mu.Unlock()
				if remoteSettled {
					return catalog.Conflict{}
				}
				continue
			}
			if phase == nil && !remoteSettled && txOwnerOf(ref).foreignPending() {
				op.session.mu.Unlock()
				e.mu.Unlock()
				if scope != nil {
					if err := scope.finishForeignStatements(nil); err != nil {
						return err
					}
				}
				if err := txOwnerOf(ref).commitForeign(op.Context(), ref); err != nil {
					return err
				}
				remoteSettled = true
				goto emptyFence
			}
			// Every replacement and veto precedes this first installation.
			if phase != nil && phase.preparing {
				e.installPreparedLocked(phase)
			} else {
				e.installNotificationsLocked(notifications)
				e.visibility.SerializableCommittedLocked(ref, e.visibility.CommitSeq())
				e.visibility.RecordCommitTimestampLocked(ref)
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
		var nextCatalog *catalog.State
		var err error
		if c != nil {
			nextCatalog, err = e.preparePrivateCatalog(op.Context(), c, ref.Epoch())
			if err != nil {
				return err
			}
		}
		prepared := make([]rowPublication, 0, len(writes)+len(deletes))
		e.mu.Lock()
		if ref.Epoch() != e.catalog.state.Epoch() {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		current, frontier := e.catalog.state, e.visibility.CommitSeq()
		var indexStores map[ids.ObjectAddress]*storage.Relation
		if len(e.storage.IndexRoots) != 0 || c != nil {
			captureCatalog := current
			if nextCatalog != nil {
				captureCatalog = nextCatalog
			}
			indexStores = captureIndexStores(e.storage.Relations, captureCatalog)
		}
		indexPredecessors := e.storage.IndexRoots
		var nextSequences map[ids.ObjectAddress]*storage.SequenceGenerator
		var nextStorage map[ids.ObjectAddress]*storage.Relation
		var materializedTokens map[ids.ObjectAddress]*storage.MaterializedLineage
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
							materializedTokens = make(map[ids.ObjectAddress]*storage.MaterializedLineage)
						}
						token := shape.original.MaterializedLineage
						if token == nil {
							token = &storage.MaterializedLineage{}
						}
						materializedTokens[address] = token
					}
				}
				nextSequences = make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(e.storage.Sequences)+len(c.generators))
				for a, g := range e.storage.Sequences {
					nextSequences[a] = g
				}
				nextStorage = make(map[ids.ObjectAddress]*storage.Relation, len(e.storage.Relations))
				for a, store := range e.storage.Relations {
					nextStorage[a] = store
				}
			}
		}
		capture := func(a ids.ObjectAddress, rows []storage.RowVersion, deletion *visibility.PrivateDeletes) error {
			original := e.storage.Relations[a]
			store := e.privateStorageLocked(c, a)
			originalRelation, live := e.catalog.state.Relation(a)
			relation := originalRelation
			if c != nil {
				relation, live = nextCatalog.Relation(a)
			}
			if store == nil || !live || a.SubID != 0 {
				return ids.ErrCatalogReplaced
			}
			prepared = append(prepared, rowPublication{address: a, store: store, original: original, originalRelation: originalRelation, relation: relation, root: store.Versions, writes: rows, ends: store.Ends, deletion: deletion, generation: store.Generation, singleSlot: -1})
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
				disclosure.Catalog = e.catalog.read(nextCatalog)
				disclosure.Recapture(disclosure.Catalog)
				if c != nil && c.roles != nil {
					disclosure.RoleRecord = c.roles
					disclosure.Recapture(disclosure.Catalog)
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
				if a.ClassID == ids.NamespaceClassID && current.Get(a) != d {
					detached := *nextCatalog
					nextCatalog = &detached
				}
			}
			if materialize {
				for a, g := range c.generators {
					if nextCatalog.Get(a) != nil {
						nextSequences[a] = g
					}
				}
			}
			for a, d := range c.descriptors {
				if a.ClassID == ids.NamespaceClassID && current.Get(a) != d {
					if err := nextCatalog.AdvanceNamespaceGeneration(); err != nil {
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
		var indexRoots map[ids.ObjectAddress]*storage.IndexRoot
		if len(indexPredecessors) != 0 || c != nil {
			buildStores := indexStores
			var rebuildIndexes map[ids.ObjectAddress]bool
			if c != nil && len(c.storage) > 0 {
				buildStores = make(map[ids.ObjectAddress]*storage.Relation, len(indexStores))
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
			var rebuildIntents map[ids.ObjectAddress]*catalog.Relation
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
						runtimeOwners[address] = shape.relation.Owner
					}
				}
			}
			indexRoots, indexErr = e.prepareIndexPublication(&EvalContext{Context: op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: input, Output: output}, current, indexCatalog, buildStores, indexPredecessors, rebuildIndexes, rebuildIntents, prepared, materialize, output, &disclosure, runtimeOwners, indexCandidates)
			if indexErr != nil {
				return indexErr
			}
		}
	publicationFence:
		session := op.session
		e.mu.Lock()
		session.mu.Lock()
		err = publicationOperationLocked(e, op)
		if err == nil && ref.Epoch() != e.catalog.state.Epoch() {
			err = ids.ErrCatalogReplaced
		}
		if err == nil && e.visibility.CommitSeq() == ids.CommitSeq(^uint64(0)) {
			err = errors.New("stuntgres: commit sequence exhausted")
		}
		changed := !e.notificationPublicationUnchangedLocked(notifications) || e.catalog.state != current || c != nil && e.visibility.CommitSeq() != frontier || !indexStoresUnchanged(e.storage.Relations, indexStores, current, indexCatalog, indexRoots, indexPredecessors)
		if err == nil {
			for _, p := range prepared {
				if e.storage.Relations[p.address] != p.original || p.store.Generation != p.generation || p.store.Ends != p.ends || len(p.store.Versions) != len(p.root) {
					changed = true
					break
				}
				current, ok := e.catalog.state.Relation(p.address)
				if p.originalRelation == nil {
					if e.catalog.state.Get(p.address) != nil {
						err = ids.ErrCatalogReplaced
						break
					}
				} else if !ok || !sameRelationRowShape(p.originalRelation, current) {
					err = ids.ErrCatalogReplaced
					break
				}
				if p.store.Generation == ^uint64(0) {
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
			if remoteSettled {
				return catalog.Conflict{}
			}
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
		if err := e.visibility.SerializablePrecommitLocked(ref); err != nil {
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
		if phase == nil && !remoteSettled && txOwnerOf(ref).foreignPending() {
			session.mu.Unlock()
			e.mu.Unlock()
			if scope != nil {
				if err := scope.finishForeignStatements(nil); err != nil {
					return err
				}
			}
			if err := txOwnerOf(ref).commitForeign(op.Context(), ref); err != nil {
				return err
			}
			remoteSettled = true
			goto publicationFence
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
				row.Born, row.Base = e.visibility.CommitSeq()+1, 0
				row.IndexAdmission = nil
				row.IndexOutputs = nil
				if row.PendingKeyChange {
					row.KeyChangedAt = row.Born
					row.PendingKeyChange = false
				}
				p.next[slot] = row
			}
			if p.nextEnds != nil {
				for id, candidate := range p.deletion.Rows {
					p.nextEnds.Rows[id] = storage.RowEnd{Version: candidate.Version, End: e.visibility.CommitSeq() + 1}
				}
				p.store.Ends = p.nextEnds
			}
			p.store.Versions = p.next
			p.store.Generation++
		}
		if c != nil {
			for address, shape := range c.storage {
				if shape.truncated && shape.original != nil {
					shape.original.TruncateLineage = shape.replacement.TruncateLineage
				}
				if token := materializedTokens[address]; token != nil {
					shape.original.MaterializedLineage = token
					shape.replacement.MaterializedLineage = token
				}
				if shape.rewritten && shape.relation.RelationKind == catalog.MaterializedViewRelation {
					for i := range shape.replacement.Versions {
						shape.replacement.Versions[i].Born = e.visibility.CommitSeq() + 1
					}
				}
			}
			e.storage.ReplaceSequencesLocked(nextSequences)
			e.storage.ReplaceRelationsLocked(nextStorage)
			e.catalog.state = nextCatalog
			e.catalog.nextRevision = nextRevision
			if c.roles != nil {
				e.roles.published = c.roles
			}
			for a, d := range c.descriptors {
				if ns, ok := d.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == session.token {
					session.temporaryNamespace = a
				}
			}
		}
		preserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.storage.IndexRoots)
		e.storage.ReplaceIndexRootsLocked(indexRoots)
		e.visibility.AdvanceCommitSeqLocked()
		e.visibility.RecordCommitTimestampLocked(ref)
		e.visibility.SerializableCommittedLocked(ref, e.visibility.CommitSeq())
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
