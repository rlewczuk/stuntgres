package engine

import (
	"errors"
	"maps"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func (e *Engine) publishWrites(op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, scope *StatementScope) error {
	owner := session.TxOwnerOf(ref)
	if op == nil || op.Self != op || op.Session == nil || op.Session.Transactions == nil || owner == nil || ref.Domain() != domainOf(e) || ref.Session() != op.Session.SessionToken || owner != op.Session.Transactions || ref.ID() == 0 || ref != owner.Ref {
		return errors.New("stuntgres: foreign or stale publication transaction")
	}
	if owner.State != session.TxActive && owner.State != session.TxImplicit {
		return errors.New("stuntgres: inactive publication transaction")
	}
	return e.publishRows(op, ref, writes, deletes, owner.Catalog, nil, scope)
}

func (e *Engine) publishRows(op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal, authority *catalogAuthority, scope *StatementScope) error {
	var err error
	writes, deletes, c, err = e.prepareTemporaryCommit(op, ref, writes, deletes, c)
	if err != nil {
		return err
	}
	return e.publishRowsPrepared(op, ref, writes, deletes, c, authority, nil, scope)
}

// A reserved empty container is not a write. PREPARE is the explicit exception:
// even an otherwise empty prepared transaction needs a retained public identity.
func publicationNeedsXID(writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal, phase *session.PreparedPublication) bool {
	if phase != nil && phase.Preparing {
		return true
	}
	if c != nil && (len(c.Descriptors) != 0 || len(c.Names) != 0 || len(c.DefaultACLs) != 0 ||
		len(c.Generators) != 0 || len(c.Storage) != 0 || c.Roles != nil ||
		index.JournalOf(c.Indexes).Pending()) {
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

func (e *Engine) publishRowsPrepared(op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal, authority *catalogAuthority, phase *session.PreparedPublication, scope *StatementScope) error {
	materialize := phase == nil || !phase.Preparing
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
			notifications := e.prepareNotificationPublication(op.Session, actions, events)
		emptyFence:
			e.mu.Lock()
			if scope != nil {
				if err := scope.validateInheritanceRelationshipsLocked(e); err != nil {
					e.mu.Unlock()
					return err
				}
			}
			op.Session.Mu.Lock()
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
			if err == nil && op.DiscardRoleResetPending() {
				err = session.ValidateRoleResetLocked(op.Session.Authorization, op.DiscardRoleReset, e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published))
			}
			if err == nil {
				err = e.visibility.SerializablePrecommitLocked(ref)
			}
			if err != nil {
				op.Session.Mu.Unlock()
				e.mu.Unlock()
				return err
			}
			if !e.notificationPublicationUnchangedLocked(notifications) {
				op.Session.Mu.Unlock()
				e.mu.Unlock()
				if remoteSettled {
					return catalog.Conflict{}
				}
				continue
			}
			if err := notificationPublicationError(notifications); err != nil {
				op.Session.Mu.Unlock()
				e.mu.Unlock()
				return err
			}
			if phase == nil && !remoteSettled && foreignPending(session.TxOwnerOf(ref)) {
				op.Session.Mu.Unlock()
				e.mu.Unlock()
				if scope != nil {
					if err := scope.finishForeignStatements(nil); err != nil {
						return err
					}
				}
				if err := commitForeign(session.TxOwnerOf(ref), op.Context(), ref); err != nil {
					return err
				}
				remoteSettled = true
				goto emptyFence
			}
			// Every replacement and veto precedes this first installation.
			if phase != nil && phase.Preparing {
				e.installPreparedLocked(phase)
			} else {
				e.installNotificationPublicationLocked(notifications)
				e.visibility.SerializableCommittedLocked(ref, e.visibility.CommitSeq())
				e.visibility.RecordCommitTimestampLocked(ref)
				e.finishPreparedLocked(phase)
				op.FinishDiscardRoleResetLocked()
			}
			op.Session.Mu.Unlock()
			e.mu.Unlock()
			return nil
		}
	}
	input, output := op.Session.Settings.Presentation()
	if scope != nil {
		input, output = scope.input, scope.output
	}
	sourceCatalog := c
	for {
		c = sourceCatalog
		notifications := e.prepareNotificationPublication(op.Session, actions, events)
		var nextCatalog *catalog.State
		var err error
		if c != nil {
			nextCatalog, err = e.preparePrivateCatalog(op.Context(), c, ref.Epoch())
			if err != nil {
				return err
			}
		}
		prepared := make([]index.RowPublication, 0, len(writes)+len(deletes))
		e.mu.Lock()
		if ref.Epoch() != e.catalog.state.Epoch() {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		c, err = e.publicationRolesLocked(op.Context(), c)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		mergedRoles := c != nil && sourceCatalog != nil && c.Roles != sourceCatalog.Roles
		currentRoles := e.roles.published
		current, frontier := e.catalog.state, e.visibility.CommitSeq()
		var indexStores map[ids.ObjectAddress]*storage.Relation
		if len(e.storage.IndexRoots) != 0 || c != nil {
			captureCatalog := current
			if nextCatalog != nil {
				captureCatalog = nextCatalog
			}
			indexStores = index.CaptureIndexStores(e.storage.Relations, captureCatalog)
		}
		indexPredecessors := e.storage.IndexRoots
		var nextSequences map[ids.ObjectAddress]*storage.SequenceGenerator
		var nextStorage map[ids.ObjectAddress]*storage.Relation
		var materializedTokens map[ids.ObjectAddress]*storage.MaterializedLineage
		if c != nil {
			if c.CachedBase != current {
				e.mu.Unlock()
				continue
			}
			if err = e.catalogPreflightLocked(c); err != nil {
				e.mu.Unlock()
				return err
			}
			if materialize {
				for address, shape := range c.Storage {
					if shape.MaterializedContinuous && shape.Original != nil {
						if materializedTokens == nil {
							materializedTokens = make(map[ids.ObjectAddress]*storage.MaterializedLineage)
						}
						token := shape.Original.MaterializedLineage
						if token == nil {
							token = &storage.MaterializedLineage{}
						}
						materializedTokens[address] = token
					}
				}
				nextSequences = make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(e.storage.Sequences)+len(c.Generators))
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
			prepared = append(prepared, index.RowPublication{Address: a, Store: store, Original: original, OriginalRelation: originalRelation, Relation: relation, Root: store.Versions, Writes: rows, Ends: store.Ends, Deletion: deletion, Generation: store.Generation, SingleSlot: -1})
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
				if c != nil && c.Roles != nil {
					disclosure.RoleRecord = c.Roles
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
			// folded calls and RI-local domain calls whose exec.Context ended.
			if err := scope.readmitPreparedPrograms(disclosure); err != nil {
				return err
			}
		}
		if c != nil {
			for a, shape := range c.Storage {
				if materialize {
					nextStorage[a] = shape.Replacement
				}
			}
			for a, d := range c.Descriptors {
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
				for a, g := range c.Generators {
					if nextCatalog.Get(a) != nil {
						nextSequences[a] = g
					}
				}
			}
			for a, d := range c.Descriptors {
				if a.ClassID == ids.NamespaceClassID && current.Get(a) != d {
					if err := nextCatalog.AdvanceNamespaceGeneration(); err != nil {
						return err
					}
					break
				}
			}
		}
		for i := range prepared {
			if err := index.PrepareRowPublication(&prepared[i], e.catalog.Domain, ref, materialize); err != nil {
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
			if c != nil && len(c.Storage) > 0 {
				buildStores = make(map[ids.ObjectAddress]*storage.Relation, len(indexStores))
				for a, store := range indexStores {
					buildStores[a] = store
				}
				rebuildIndexes = make(map[ids.ObjectAddress]bool, len(c.Storage))
				for a, shape := range c.Storage {
					buildStores[a] = shape.Replacement
					rebuildIndexes[a] = true
				}
			}
			var indexErr error
			var rebuildIntents map[ids.ObjectAddress]*catalog.Relation
			var indexCandidates map[ids.ObjectAddress]index.RootCandidate
			if c != nil {
				rebuildIntents = index.JournalIndexesOrZero(c).Rebuilds
				indexCandidates = index.JournalIndexesOrZero(c).Candidates
			}
			var runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef
			if c != nil {
				for address, shape := range c.Storage {
					if shape.MaterializedConcurrent {
						if runtimeOwners == nil {
							runtimeOwners = make(map[ids.ObjectAddress]authorization.PrincipalRef)
						}
						runtimeOwners[address] = shape.Relation.Owner
					}
				}
			}
			indexRoots, indexErr = e.prepareIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope), Input: input, Output: output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, current, indexCatalog, buildStores, indexPredecessors, rebuildIndexes, rebuildIntents, prepared, materialize, output, &disclosure, runtimeOwners, indexCandidates)
			if indexErr != nil {
				return indexErr
			}
		}
	publicationFence:
		sess := op.Session
		e.mu.Lock()
		if scope != nil {
			if err := scope.validateInheritanceRelationshipsLocked(e); err != nil {
				e.mu.Unlock()
				return err
			}
		}
		sess.Mu.Lock()
		err = publicationOperationLocked(e, op)
		if err == nil && ref.Epoch() != e.catalog.state.Epoch() {
			err = ids.ErrCatalogReplaced
		}
		if err == nil && e.visibility.CommitSeq() == ids.CommitSeq(^uint64(0)) {
			err = errors.New("stuntgres: commit sequence exhausted")
		}
		changed := e.roles.published != currentRoles || !e.notificationPublicationUnchangedLocked(notifications) || e.catalog.state != current || c != nil && e.visibility.CommitSeq() != frontier || !index.StoresUnchanged(e.storage.Relations, indexStores, current, indexCatalog, indexRoots, indexPredecessors)
		if err == nil {
			for _, p := range prepared {
				if e.storage.Relations[p.Address] != p.Original || p.Store.Generation != p.Generation || p.Store.Ends != p.Ends || len(p.Store.Versions) != len(p.Root) {
					changed = true
					break
				}
				current, ok := e.catalog.state.Relation(p.Address)
				if p.OriginalRelation == nil {
					if e.catalog.state.Get(p.Address) != nil {
						err = ids.ErrCatalogReplaced
						break
					}
				} else if !ok || !index.SameRelationRowShape(p.OriginalRelation, current) {
					err = ids.ErrCatalogReplaced
					break
				}
				if p.Store.Generation == ^uint64(0) {
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
		if err == nil && op.DiscardRoleResetPending() {
			err = session.ValidateRoleResetLocked(op.Session.Authorization, op.DiscardRoleReset, e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published))
		}
		if err == nil && mergedRoles {
			c.Roles.Revision, err = e.roles.revisionCandidateLocked()
		}
		if err != nil {
			sess.Mu.Unlock()
			e.mu.Unlock()
			return err
		}
		if changed {
			sess.Mu.Unlock()
			e.mu.Unlock()
			if remoteSettled {
				return catalog.Conflict{}
			}
			continue
		}
		nextRevision := e.catalog.nextRevision
		if c != nil && materialize {
			nextRevision = catalog.RevisionFrontier(nextRevision, maps.All(c.Descriptors))
		}
		if err := e.visibility.SerializablePrecommitLocked(ref); err != nil {
			sess.Mu.Unlock()
			e.mu.Unlock()
			return err
		}
		if err := notificationPublicationError(notifications); err != nil {
			sess.Mu.Unlock()
			e.mu.Unlock()
			return err
		}
		if phase != nil && phase.Preparing {
			e.installPreparedLocked(phase)
			sess.Mu.Unlock()
			e.mu.Unlock()
			return nil
		}
		if phase == nil && !remoteSettled && foreignPending(session.TxOwnerOf(ref)) {
			sess.Mu.Unlock()
			e.mu.Unlock()
			if scope != nil {
				if err := scope.finishForeignStatements(nil); err != nil {
					return err
				}
			}
			if err := commitForeign(session.TxOwnerOf(ref), op.Context(), ref); err != nil {
				return err
			}
			remoteSettled = true
			goto publicationFence
		}
		rolePublication, err := e.journalRolePublicationLocked(c, true)
		if err != nil {
			sess.Mu.Unlock()
			e.mu.Unlock()
			return err
		}
		var databaseRoots map[*Engine]*catalog.State
		if c != nil {
			databaseRoots = e.databaseProjectionLocked(nextCatalog)
		}
		// No callback, allocation or failure remains after this point.
		e.installNotificationPublicationLocked(notifications)
		if c != nil {
			for _, shape := range c.Storage {
				if shape.Original != nil {
					shape.Replacement.Maintenance = shape.Original.Maintenance
					if shape.Truncated {
						shape.Replacement.Maintenance.FrozenXID = 0
						shape.Replacement.Maintenance.Live = 0
						shape.Replacement.Maintenance.Dead = 0
						shape.Replacement.Maintenance.InsertsSince = 0
						shape.Replacement.Maintenance.Scanned = false
						shape.Replacement.Maintenance.Tuples = 0
					}
					if shape.Rewritten && shape.Relation.RelationKind == catalog.MaterializedViewRelation {
						shape.Replacement.Maintenance.FrozenXID = 0
						shape.Replacement.Maintenance.Live = 0
						shape.Replacement.Maintenance.Dead = 0
					}
				}
			}
		}
		for _, p := range prepared {
			for i, row := range p.Writes {
				slot := len(p.Root) + i
				if p.Slots != nil {
					slot = p.Slots[i]
				}
				if p.SingleSlot >= 0 {
					slot = p.SingleSlot
				}
				if row.Base == 0 {
					p.Store.Maintenance.Inserts++
					p.Store.Maintenance.InsertsSince++
					p.Store.Maintenance.Live++
				} else {
					p.Store.Maintenance.Updates++
				}
				p.Store.Maintenance.Modifications++
				row.Born, row.Base = e.visibility.CommitSeq()+1, 0
				row.InsertFullXID = e.visibility.FullXIDLocked(ref)
				p.Store.Maintenance.RecordBirth(row.InsertFullXID)
				row.Frozen, row.ImportFrozen = false, false
				row.IndexAdmission = nil
				row.IndexOutputs = nil
				if row.PendingKeyChange {
					row.KeyChangedAt = row.Born
					row.PendingKeyChange = false
				}
				p.Next[slot] = row
			}
			if p.NextEnds != nil {
				for id, candidate := range p.Deletion.Rows {
					p.Store.Maintenance.Deletes++
					p.Store.Maintenance.Modifications++
					p.Store.Maintenance.Live--
					p.Store.Maintenance.Dead++
					p.NextEnds.Rows[id] = storage.RowEnd{Version: candidate.Version, End: e.visibility.CommitSeq() + 1, Moved: candidate.Moved}
				}
				p.Store.Ends = p.NextEnds
			}
			p.Store.Versions = p.Next
			p.Store.Generation++
			e.visibility.InitializeMaintenanceLocked(p.Store)
		}
		if c != nil {
			for address, shape := range c.Storage {
				if shape.Truncated && shape.Original != nil {
					shape.Original.TruncateLineage = shape.Replacement.TruncateLineage
				}
				if token := materializedTokens[address]; token != nil {
					shape.Original.MaterializedLineage = token
					shape.Replacement.MaterializedLineage = token
				}
				if shape.Rewritten && shape.Relation.RelationKind == catalog.MaterializedViewRelation {
					for i := range shape.Replacement.Versions {
						shape.Replacement.Versions[i].Born = e.visibility.CommitSeq() + 1
						shape.Replacement.Versions[i].InsertFullXID = e.visibility.FullXIDLocked(ref)
						shape.Replacement.Maintenance.RecordBirth(shape.Replacement.Versions[i].InsertFullXID)
						shape.Replacement.Versions[i].Frozen = false
						shape.Replacement.Versions[i].ImportFrozen = false
					}
					n := int64(len(shape.Replacement.Versions))
					shape.Replacement.Maintenance.Inserts += n
					shape.Replacement.Maintenance.InsertsSince += n
					shape.Replacement.Maintenance.Modifications += n
					shape.Replacement.Maintenance.Live = n
				}
				e.visibility.InitializeMaintenanceLocked(shape.Replacement)
			}
			e.storage.ReplaceSequencesLocked(nextSequences)
			e.storage.ReplaceRelationsLocked(nextStorage)
			e.catalog.state = nextCatalog
			installDatabaseProjectionLocked(databaseRoots)
			e.catalog.nextRevision = nextRevision
			if c.Roles != nil {
				e.roles.published = rolePublication
				if rolePublication.Revision >= e.roles.nextRevision {
					e.roles.nextRevision = rolePublication.Revision
					if rolePublication.Revision != ^uint64(0) {
						e.roles.nextRevision++
					}
				}
			}
			for a, d := range c.Descriptors {
				if ns, ok := d.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == sess.SessionToken {
					sess.TemporaryNamespace = a
				}
			}
		}
		index.PreserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.storage.IndexRoots)
		e.storage.ReplaceIndexRootsLocked(indexRoots)
		e.visibility.AdvanceCommitSeqLocked()
		e.visibility.RecordCommitTimestampLocked(ref)
		e.visibility.SerializableCommittedLocked(ref, e.visibility.CommitSeq())
		e.finishPreparedLocked(phase)
		op.FinishDiscardRoleResetLocked()
		sess.Mu.Unlock()
		e.mu.Unlock()
		return nil
	}
}

// Caller holds Session.mu; row/catalog publication additionally holds Engine.mu.
func publicationOperationLocked(e *Engine, op *session.Operation) error {
	return session.PublicationOperationLocked(domainOf(e), op)
}
