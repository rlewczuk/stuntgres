package core

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
	if op == nil || op.Self != op || op.Session == nil || op.Session.Transactions == nil || owner == nil || ref.Domain() != DomainOf(e) || ref.Session() != op.Session.SessionToken || owner != op.Session.Transactions || ref.ID() == 0 || ref != owner.Ref {
		return errors.New("stuntgres: foreign or stale publication transaction")
	}
	if owner.State != session.TxActive && owner.State != session.TxImplicit {
		return errors.New("stuntgres: inactive publication transaction")
	}
	return e.publishRows(op, ref, writes, deletes, owner.Catalog, nil, scope)
}

func (e *Engine) publishRows(op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal, authority *session.CatalogAuthority, scope *StatementScope) error {
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
		len(c.Generators) != 0 || len(c.Storage) != 0 || len(c.Statistics) != 0 || c.Roles != nil ||
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

func (e *Engine) publishRowsPrepared(op *session.Operation, ref ids.TxRef, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes, c *session.Journal, authority *session.CatalogAuthority, phase *session.PreparedPublication, scope *StatementScope) error {
	materialize := phase == nil || !phase.Preparing
	actions, events, err := notificationEffects(op, ref, phase, scope)
	if err != nil {
		return err
	}
	if len(events) != 0 || publicationNeedsXID(writes, deletes, c, phase) {
		e.Mu.Lock()
		err := e.AssignTransactionXIDLocked(ref)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
	}
	remoteSettled := false
	if len(writes) == 0 && len(deletes) == 0 && c == nil {
		for {
			notifications := e.prepareNotificationPublication(op.Session, actions, events)
		emptyFence:
			e.Mu.Lock()
			if scope != nil {
				if err := scope.validateInheritanceRelationshipsLocked(e); err != nil {
					e.Mu.Unlock()
					return err
				}
			}
			op.Session.Mu.Lock()
			err := PublicationOperationLocked(e, op)
			if err == nil && ref.Epoch() != e.Catalog.State.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			if err == nil {
				err = e.catalogAuthorityPreflightLocked(authority, nil)
			}
			if err == nil {
				err = e.preparedPublicationPreflightLocked(op, phase)
			}
			if err == nil && op.DiscardRoleResetPending() {
				err = session.ValidateRoleResetLocked(op.Session.Authorization, op.DiscardRoleReset, e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()))
			}
			if err == nil {
				err = e.Visibility.SerializablePrecommitLocked(ref)
			}
			if err != nil {
				op.Session.Mu.Unlock()
				e.Mu.Unlock()
				return err
			}
			if !e.notificationPublicationUnchangedLocked(notifications) {
				op.Session.Mu.Unlock()
				e.Mu.Unlock()
				if remoteSettled {
					return catalog.Conflict{}
				}
				continue
			}
			if err := notificationPublicationError(notifications); err != nil {
				op.Session.Mu.Unlock()
				e.Mu.Unlock()
				return err
			}
			if phase == nil && !remoteSettled && foreignPending(session.TxOwnerOf(ref)) {
				op.Session.Mu.Unlock()
				e.Mu.Unlock()
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
				e.Visibility.SerializableCommittedLocked(ref, e.Visibility.CommitSeq())
				e.Visibility.RecordCommitTimestampLocked(ref)
				e.finishPreparedLocked(phase)
				op.FinishDiscardRoleResetLocked()
			}
			op.Session.Mu.Unlock()
			e.Mu.Unlock()
			return nil
		}
	}
	input, output := op.EffectiveSettings().Presentation()
	if scope != nil {
		input, output = scope.Input, scope.Output
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
		e.Mu.Lock()
		if ref.Epoch() != e.Catalog.State.Epoch() {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		c, err = e.publicationRolesLocked(op.Context(), c)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		mergedRoles := c != nil && sourceCatalog != nil && c.Roles != sourceCatalog.Roles
		currentRoles := e.RoleStore.Published
		current, frontier := e.Catalog.State, e.Visibility.CommitSeq()
		var indexStores map[ids.ObjectAddress]*storage.Relation
		if len(e.Storage.IndexRoots) != 0 || c != nil {
			captureCatalog := current
			if nextCatalog != nil {
				captureCatalog = nextCatalog
			}
			indexStores = index.CaptureIndexStores(e.Storage.Relations, captureCatalog)
		}
		indexPredecessors := e.Storage.IndexRoots
		var nextSequences map[ids.ObjectAddress]*storage.SequenceGenerator
		var nextStorage map[ids.ObjectAddress]*storage.Relation
		var materializedTokens map[ids.ObjectAddress]*storage.MaterializedLineage
		if c != nil {
			if c.CachedBase != current {
				e.Mu.Unlock()
				continue
			}
			if err = e.CatalogPreflightLocked(c); err != nil {
				e.Mu.Unlock()
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
				nextSequences = make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(e.Storage.Sequences)+len(c.Generators))
				for a, g := range e.Storage.Sequences {
					nextSequences[a] = g
				}
				nextStorage = make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))
				for a, store := range e.Storage.Relations {
					nextStorage[a] = store
				}
			}
		}
		capture := func(a ids.ObjectAddress, rows []storage.RowVersion, deletion *visibility.PrivateDeletes) error {
			original := e.Storage.Relations[a]
			store := e.PrivateStorageLocked(c, a)
			originalRelation, live := e.Catalog.State.Relation(a)
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
			disclosure, err = e.AuthorizationLocked(op)
			if nextCatalog != nil {
				disclosure.Catalog = e.Catalog.Read(nextCatalog)
				disclosure.Recapture(disclosure.Catalog)
				if c != nil && c.Roles != nil {
					disclosure.RoleRecord = c.Roles
					disclosure.Recapture(disclosure.Catalog)
				}
			}
		}
		e.Mu.Unlock()
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
			if err := index.PrepareRowPublication(&prepared[i], e.Catalog.Domain, ref, materialize); err != nil {
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
			indexRoots, indexErr = e.prepareIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: HostOf(scope), Input: input, Output: output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, current, indexCatalog, buildStores, indexPredecessors, rebuildIndexes, rebuildIntents, prepared, materialize, output, &disclosure, runtimeOwners, indexCandidates, index.ProgressBinding{})
			if indexErr != nil {
				return indexErr
			}
		}
	publicationFence:
		sess := op.Session
		e.Mu.Lock()
		if scope != nil {
			if err := scope.validateInheritanceRelationshipsLocked(e); err != nil {
				e.Mu.Unlock()
				return err
			}
		}
		sess.Mu.Lock()
		err = PublicationOperationLocked(e, op)
		if err == nil && ref.Epoch() != e.Catalog.State.Epoch() {
			err = ids.ErrCatalogReplaced
		}
		if err == nil && e.Visibility.CommitSeq() == ids.CommitSeq(^uint64(0)) {
			err = errors.New("stuntgres: commit sequence exhausted")
		}
		changed := e.RoleStore.Published != currentRoles || !e.notificationPublicationUnchangedLocked(notifications) || e.Catalog.State != current || c != nil && e.Visibility.CommitSeq() != frontier || !index.StoresUnchanged(e.Storage.Relations, indexStores, current, indexCatalog, indexRoots, indexPredecessors)
		if err == nil {
			for _, p := range prepared {
				if e.Storage.Relations[p.Address] != p.Original || p.Store.Generation != p.Generation || p.Store.Ends != p.Ends || len(p.Store.Versions) != len(p.Root) {
					changed = true
					break
				}
				current, ok := e.Catalog.State.Relation(p.Address)
				if p.OriginalRelation == nil {
					if e.Catalog.State.Get(p.Address) != nil {
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
			err = e.CatalogPreflightLocked(c)
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
			err = session.ValidateRoleResetLocked(op.Session.Authorization, op.DiscardRoleReset, e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()))
		}
		if err == nil && mergedRoles {
			c.Roles.Revision, err = e.RoleStore.RevisionCandidateLocked()
		}
		if err != nil {
			sess.Mu.Unlock()
			e.Mu.Unlock()
			return err
		}
		if changed {
			sess.Mu.Unlock()
			e.Mu.Unlock()
			if remoteSettled {
				return catalog.Conflict{}
			}
			continue
		}
		nextRevision := e.Catalog.NextRevision()
		if c != nil && materialize {
			nextRevision = catalog.RevisionFrontier(nextRevision, maps.All(c.Descriptors))
		}
		if err := e.Visibility.SerializablePrecommitLocked(ref); err != nil {
			sess.Mu.Unlock()
			e.Mu.Unlock()
			return err
		}
		if err := notificationPublicationError(notifications); err != nil {
			sess.Mu.Unlock()
			e.Mu.Unlock()
			return err
		}
		if phase != nil && phase.Preparing {
			e.installPreparedLocked(phase)
			sess.Mu.Unlock()
			e.Mu.Unlock()
			return nil
		}
		if phase == nil && !remoteSettled && foreignPending(session.TxOwnerOf(ref)) {
			sess.Mu.Unlock()
			e.Mu.Unlock()
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
			e.Mu.Unlock()
			return err
		}
		var sharedRoots catalog.SharedProjection
		if c != nil {
			sharedRoots = e.Catalog.ProjectSharedLocked(nextCatalog, e.Cluster.Catalogs)
		}
		var relationMetadata map[ids.ObjectAddress]*storage.RelationMetadata
		if c != nil {
			relationMetadata = e.prepareRelationMetadataLocked(nextCatalog, c)
		}
		// No callback, allocation or failure remains after this point.
		e.installNotificationPublicationLocked(notifications)
		if c != nil {
			for _, shape := range c.Storage {
				if shape.Original != nil {
					scanned, tuples := shape.Replacement.Maintenance.Scanned, shape.Replacement.Maintenance.Tuples
					shape.Replacement.Maintenance = shape.Original.Maintenance
					if shape.Truncated {
						shape.Replacement.Maintenance.FrozenXID = 0
						shape.Replacement.Maintenance.Live = 0
						shape.Replacement.Maintenance.Dead = 0
						shape.Replacement.Maintenance.InsertsSince = 0
						shape.Replacement.Maintenance.Scanned = false
						shape.Replacement.Maintenance.Tuples = 0
					}
					if shape.TruncateScanReset {
						shape.Replacement.Maintenance.Scanned = scanned
						shape.Replacement.Maintenance.Tuples = 0
						if scanned {
							shape.Replacement.Maintenance.Tuples = tuples
						}
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
				row.Born, row.Base = e.Visibility.CommitSeq()+1, 0
				row.InsertFullXID = e.Visibility.FullXIDLocked(ref)
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
					p.NextEnds.Rows[id] = storage.RowEnd{Version: candidate.Version, End: e.Visibility.CommitSeq() + 1, Moved: candidate.Moved}
				}
				p.Store.Ends = p.NextEnds
			}
			p.Store.Versions = p.Next
			p.Store.Generation++
			e.Visibility.InitializeMaintenanceLocked(p.Store)
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
						shape.Replacement.Versions[i].Born = e.Visibility.CommitSeq() + 1
						shape.Replacement.Versions[i].InsertFullXID = e.Visibility.FullXIDLocked(ref)
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
				if shape.Relation.RelationKind.Stored() {
					e.Visibility.InitializeMaintenanceLocked(shape.Replacement)
				}
			}
			e.Storage.ReplaceSequencesLocked(nextSequences)
			e.Storage.ReplaceRelationsLocked(nextStorage)
			e.Storage.RelationMetadata = relationMetadata
			e.Catalog.PublishLocked(nextCatalog)
			sharedRoots.InstallLocked()
			e.Catalog.InstallRevisionFrontierLocked(nextRevision)
			if c.Roles != nil {
				e.RoleStore.PublishLocked(rolePublication)
				e.RoleStore.AdvanceRevisionFrontierLocked(rolePublication.Revision)
			}
			for a, d := range c.Descriptors {
				if ns, ok := d.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == sess.SessionToken {
					sess.TemporaryNamespace = a
				}
			}
		}
		index.PreserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.Storage.IndexRoots)
		e.Storage.ReplaceIndexRootsLocked(indexRoots)
		e.Visibility.AdvanceCommitSeqLocked()
		e.Visibility.RecordCommitTimestampLocked(ref)
		e.Visibility.SerializableCommittedLocked(ref, e.Visibility.CommitSeq())
		e.finishPreparedLocked(phase)
		op.FinishDiscardRoleResetLocked()
		sess.Mu.Unlock()
		e.Mu.Unlock()
		return nil
	}
}

// Caller holds Session.mu; row/catalog publication additionally holds Engine.mu.
func PublicationOperationLocked(e *Engine, op *session.Operation) error {
	return session.PublicationOperationLocked(DomainOf(e), op)
}
