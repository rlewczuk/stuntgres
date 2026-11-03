package core

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

type ginOwnerCapture struct {
	owner                *session.TransactionOwner
	prepared             *session.PreparedTransaction
	ref                  ids.TxRef
	revision             uint64
	writes, nextWrites   map[ids.ObjectAddress][]storage.RowVersion
	journal, nextJournal *session.Journal
	saves, nextSaves     []session.SavepointMark
	eligibleSaves        []bool
	deletes              map[ids.ObjectAddress]*visibility.PrivateDeletes
	claimed              bool
	changed              bool
}

// ginMaintenanceTarget preserves index_open's kind check before recovery and
// the AM/TEMP/owner checks. Recovery is supplied by the runtime's recovery fact;
// like pg_is_in_recovery, the embedded engine currently supplies false.
func ginMaintenanceTarget(auth AuthorizationView, target *catalog.Relation, recovery bool, token ids.SessionToken) error {
	if target.RelationKind != catalog.IndexRelation || target.SQLIndex == nil {
		return SQLError("42809", fmt.Sprintf("\"%s\" is not an index", target.Name()))
	}
	if recovery {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "recovery is in progress", Hint: "GIN pending list cannot be cleaned up during recovery."}
	}
	parent, _ := auth.Catalog.Relation(target.SQLIndex.Parent)
	if target.SQLIndex.Method != catalog.GinMethod || target.SQLIndex.MethodIdentity() != 2742 || parent != nil && parent.Partition != nil {
		return SQLError("42809", fmt.Sprintf("\"%s\" is not a GIN index", target.Name()))
	}
	if ns, ok := auth.Catalog.Namespace(target.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token {
		return SQLError("0A000", "cannot access temporary indexes of other sessions")
	}
	return auth.Check(access.Requirement{Object: auth.Ref(target.Address()), OwnerOnly: true})
}

func (h *statementHost) GinCleanPendingList(oid ids.OID) (int64, error) {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil {
		return 0, err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	e.Mu.Lock()
	auth, err := s.callableAuthorizationLocked(e)
	var target *catalog.Relation
	var ref ids.ObjectRef
	if err == nil {
		target, _ = auth.Catalog.Relation(address)
		ref = auth.Ref(address)
		if target == nil {
			err = SQLError("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return 0, err
	}
	var contribution lock.RelationContribution
	defer e.Locks.ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.RowExclusive, &contribution); err != nil {
		return 0, err
	}
	for {
		owners, err := h.LockHeapStatisticsCohort()
		if err != nil {
			return 0, err
		}
		auth, err = s.callableAuthorizationLocked(e)
		if err == nil && (ref.Epoch != auth.Catalog.Epoch() || auth.Catalog.Get(address) != target) {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			h.MarkTemporaryTargetLocked(ref)
			err = ginMaintenanceTarget(auth, target, false, s.Op.Session.SessionToken)
		}
		if err != nil {
			h.UnlockHeapStatisticsCohortLocked(owners)
			return 0, err
		}
		if !target.SQLIndex.Valid {
			h.UnlockHeapStatisticsCohortLocked(owners)
			s.Op.EmitNotice(session.LevelDebug1, sqlstate.Error{Severity: "DEBUG", SeverityUnlocalized: "DEBUG", Code: "55000", Message: fmt.Sprintf("index \"%s\" is not valid", target.Name())})
			return 0, nil
		}
		parentAddress := target.SQLIndex.Parent
		store := e.PrivateStorageLocked(s.PendingCatalog(), parentAddress)
		root := e.Storage.IndexRoots[address]
		shared := e.Storage.Relations[parentAddress] == store
		if !shared {
			root = nil
		}
		if root == nil && s.PendingCatalog() != nil {
			root = index.JournalIndexesOrZero(s.PendingCatalog()).Candidates[address].Root
		}
		if store == nil || root == nil || root.Gin == nil {
			h.UnlockHeapStatisticsCohortLocked(owners)
			return 0, ids.ErrCatalogReplaced
		}
		catalogState, frontier := e.Catalog.State, e.Visibility.CommitSeq()
		generation, ends, rows := store.Generation, store.Ends, store.Versions
		committedRoot := e.Storage.IndexRoots[address]
		roots, stores := e.Storage.IndexRoots, e.Storage.Relations
		var nextRoots map[ids.ObjectAddress]*storage.IndexRoot
		if shared && committedRoot != nil && len(root.Gin.Pending) != 0 {
			nextRoots = maps.Clone(roots)
		}
		captures := make([]ginOwnerCapture, 0, len(owners))
		ownerRefs := make([]ids.TxRef, len(owners))
		ownerStates := make([]session.TxState, len(owners))
		for i, owner := range owners {
			ownerRefs[i], ownerStates[i] = owner.Ref, owner.State
		}
		ownerCatalogs := make([]*session.Journal, len(owners))
		for i, owner := range owners {
			ownerCatalogs[i] = owner.Catalog
		}
		preparedCount := e.prepared.Len()
		seen := make(map[ids.TxRef]bool, len(owners))
		eligible := func(ref ids.TxRef, journal *session.Journal) bool {
			return ref.Domain() == s.TxRef.Domain() && ref.Epoch() == s.TxRef.Epoch() && e.PrivateStorageLocked(journal, parentAddress) == store
		}
		for _, owner := range owners {
			if (owner.State == session.TxActive || owner.State == session.TxImplicit) && eligible(owner.Ref, owner.Catalog) {
				seen[owner.Ref] = true
				captures = append(captures, ginOwnerCapture{owner: owner, ref: owner.Ref, revision: owner.GinRevision, writes: owner.Writes, journal: owner.Catalog, saves: owner.Savepoints, deletes: owner.Deletes})
				capture := &captures[len(captures)-1]
				for _, saved := range owner.Savepoints {
					capture.eligibleSaves = append(capture.eligibleSaves, eligible(owner.Ref, saved.Catalog))
				}
			}
		}
		for _, prepared := range e.prepared.All() {
			if prepared.Database == e.DatabaseName && !seen[prepared.Ref] && eligible(prepared.Ref, prepared.Catalog) {
				captures = append(captures, ginOwnerCapture{prepared: prepared, ref: prepared.Ref, revision: prepared.GinRevision, writes: prepared.Writes, journal: prepared.Catalog, deletes: prepared.Deletes, claimed: prepared.Claimed})
			}
		}
		stage, own, journal := s.Stage.Rows, s.OwnWrites, s.Catalog
		var committedIndex *catalog.Relation
		if e.Storage.Relations[parentAddress] == store {
			committedIndex, _ = e.Catalog.State.Relation(address)
		}
		targetView := auth.Catalog
		preparation := &index.GinMaintenance{Context: s.Op.Context(), Index: target, CommittedIndex: committedIndex, Store: store, Input: s.Input, View: e.Catalog.Read(catalogState), TargetView: targetView, Roots: roots, Stores: stores}
		var authorities []index.GinMaintenanceAuthority
		capturedJournals := make(map[*session.Journal]bool)
		captureAuthority := func(changes *session.Journal) {
			if capturedJournals[changes] || e.PrivateStorageLocked(changes, parentAddress) != store {
				return
			}
			capturedJournals[changes] = true
			view := e.PrivateCatalogLocked(changes)
			descriptor, exists := view.Relation(address)
			if !exists || descriptor.SQLIndex == nil {
				return
			}
			parent, exists := view.Relation(parentAddress)
			if exists {
				authorities = append(authorities, preparation.CaptureAuthority(index.GinMaintenanceAuthority{View: view, Index: descriptor, Parent: parent, Journal: changes}))
			}
		}
		captureAuthority(nil)
		captureAuthority(s.PendingCatalog())
		for _, capture := range captures {
			captureAuthority(capture.journal)
			for i, saved := range capture.saves {
				if capture.eligibleSaves[i] {
					captureAuthority(saved.Catalog)
					captureAuthority(saved.StatementCatalog)
				}
			}
		}
		h.UnlockHeapStatisticsCohortLocked(owners)
		for _, authority := range authorities {
			if err := preparation.Authorize(authority); err != nil {
				return 0, err
			}
		}
		nextRoot, err := preparation.Root(root)
		if err != nil {
			return 0, err
		}
		if nextRoots != nil {
			nextRoots[address] = nextRoot
		}
		// Authenticate every live journal before memoizing any produced row alias.
		for _, capture := range captures {
			if _, err := preparation.Journal(capture.journal); err != nil {
				return 0, err
			}
			for i, saved := range capture.saves {
				if !capture.eligibleSaves[i] {
					continue
				}
				if _, err := preparation.Journal(saved.Catalog); err != nil {
					return 0, err
				}
				if _, err := preparation.Journal(saved.StatementCatalog); err != nil {
					return 0, err
				}
			}
		}
		if _, err := preparation.Journal(journal); err != nil {
			return 0, err
		}
		incidences := preparation.PendingIncidences(root)
		identities := make(map[index.RowIdentity]bool, len(root.Entries))
		for _, entry := range root.Entries {
			identities[index.RowIdentity{Row: entry.Row, Version: entry.Version}] = true
		}
		type pendingBatch struct {
			bindings []sqltype.GinBinding
			entries  []storage.IndexEntry
		}
		private := make(map[*catalog.Relation]*pendingBatch)
		count := func(writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) error {
			for _, row := range writes[parentAddress] {
				identity := index.RowIdentity{Row: row.Row, Version: row.Version}
				if identities[identity] || !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), nil, deletes[parentAddress]) {
					continue
				}
				output, ok := row.IndexOutputs[address]
				if ok && output.Member && output.GinPending {
					bindings, _, accepted, err := preparation.OutputBindings(output, row.IndexAdmission)
					if err != nil {
						return err
					}
					if !accepted {
						continue
					}
					identities[identity] = true
					batch := private[output.Index]
					if batch == nil {
						batch = &pendingBatch{bindings: bindings}
						private[output.Index] = batch
					}
					batch.entries = append(batch.entries, storage.IndexEntry{Keys: output.Keys, Row: row.Row, Version: row.Version})
				}
			}
			return nil
		}
		for i := range captures {
			c := &captures[i]
			if err := count(c.writes, c.deletes); err != nil {
				return 0, err
			}
			c.nextWrites, err = preparation.Writes(c.writes)
			if err == nil {
				c.nextJournal, err = preparation.Journal(c.journal)
			}
			if err != nil {
				return 0, err
			}
			c.changed = !storage.SameVersionRoot(c.writes[parentAddress], c.nextWrites[parentAddress]) || c.nextJournal != c.journal
			c.nextSaves = c.saves
			for j, saved := range c.saves {
				if !c.eligibleSaves[j] {
					continue
				}
				mapped := saved
				mapped.Writes, err = preparation.Writes(saved.Writes)
				if err == nil {
					mapped.Catalog, err = preparation.Journal(saved.Catalog)
				}
				if err == nil {
					mapped.StatementCatalog, err = preparation.Journal(saved.StatementCatalog)
				}
				if err != nil {
					return 0, err
				}
				if !storage.SameVersionRoot(mapped.Writes[parentAddress], saved.Writes[parentAddress]) || mapped.Catalog != saved.Catalog || mapped.StatementCatalog != saved.StatementCatalog {
					if !c.changed || len(c.nextSaves) != 0 && &c.nextSaves[0] == &c.saves[0] {
						c.nextSaves = slices.Clone(c.saves)
					}
					c.nextSaves[j] = mapped
					c.changed = true
				}
			}
			if c.changed && c.revision == ^uint64(0) {
				return 0, fmt.Errorf("stuntgres: GIN representation revision exhausted")
			}
		}
		if err := count(stage, s.Stage.Deletes); err != nil {
			return 0, err
		}
		nextStage, err := preparation.Writes(stage)
		if err != nil {
			return 0, err
		}
		nextOwn, err := preparation.Writes(own)
		if err != nil {
			return 0, err
		}
		nextJournal, err := preparation.Journal(journal)
		if err != nil {
			return 0, err
		}
		for _, batch := range private {
			deferred, err := storage.BuildGin(s.Op.Context(), batch.bindings, batch.entries, s.Input)
			if err != nil {
				return 0, err
			}
			// BuildGin's output is main-shaped; here it measures actual already-produced
			// private tokens without installing uncommitted membership in shared roots.
			deferred.Pending = deferred.Columns
			extra := storage.GinPendingIncidences(deferred)
			maximum := int64(^uint64(0) >> 1)
			if extra > maximum-incidences {
				incidences = maximum
			} else {
				incidences += extra
			}
		}
		if err := util.ExecutionCheckpoint(s.ActivateRuntime(s.executionContext()), "gin.maintenance.prepared"); err != nil {
			return 0, err
		}
		locked, err := h.LockHeapStatisticsCohort()
		if err != nil {
			return 0, err
		}
		auth, err = s.callableAuthorizationLocked(e)
		changed := e.Catalog.State != catalogState || e.Visibility.CommitSeq() != frontier || e.Storage.IndexRoots[address] != committedRoot || store.Generation != generation || store.Ends != ends || !storage.SameVersionRoot(store.Versions, rows) || !slices.Equal(owners, locked)
		changed = changed || e.PrivateStorageLocked(s.PendingCatalog(), parentAddress) != store
		changed = changed || e.prepared.Len() != preparedCount
		if nextRoots != nil {
			changed = changed || len(nextRoots) != len(e.Storage.IndexRoots)
			for key, expected := range nextRoots {
				if key == address {
					expected = committedRoot
				}
				if e.Storage.IndexRoots[key] != expected {
					changed = true
					break
				}
			}
		}
		if !changed {
			for i, owner := range owners {
				changed = changed || owner.Ref != ownerRefs[i] || owner.State != ownerStates[i] || owner.Catalog != ownerCatalogs[i]
			}
		}
		if err == nil && auth.Catalog.Get(address) != target {
			err = ids.ErrCatalogReplaced
		}
		for _, c := range captures {
			if c.owner != nil {
				owner := c.owner
				changed = changed || owner.Ref != c.ref || owner.GinRevision != c.revision || owner.Catalog != c.journal || !sameGinWriteMaps(owner.Writes, c.writes) || !maps.Equal(owner.Deletes, c.deletes) || len(owner.Savepoints) != len(c.saves)
				if !changed {
					for i, saved := range c.saves {
						now := owner.Savepoints[i]
						changed = changed || now.Serial != saved.Serial || now.Catalog != saved.Catalog || now.StatementCatalog != saved.StatementCatalog || !sameGinWriteMaps(now.Writes, saved.Writes)
					}
				}
			} else {
				p := c.prepared
				changed = changed || e.prepared.Lookup(p.GID) != p || p.Claimed != c.claimed || p.GinRevision != c.revision || p.Catalog != c.journal || !sameGinWriteMaps(p.Writes, c.writes) || !maps.Equal(p.Deletes, c.deletes)
			}
		}
		for _, authority := range authorities {
			if !authority.SourceCurrent(e.Catalog.Current(), e.Storage.IndexRoots, e.Storage.Relations) {
				changed = true
				break
			}
		}
		if err == nil {
			err = s.Op.Context().Err()
		}
		if err != nil {
			h.UnlockHeapStatisticsCohortLocked(locked)
			return 0, err
		}
		if changed {
			h.UnlockHeapStatisticsCohortLocked(locked)
			continue
		}
		// Publish an immutable map image, fenced against every unrelated root.
		// All owner replacements and allocations are prepared before this window.
		if nextRoots != nil {
			e.Storage.ReplaceIndexRootsLocked(nextRoots)
		}
		for _, c := range captures {
			if !c.changed {
				continue
			}
			if c.owner != nil {
				c.owner.Writes, c.owner.Catalog, c.owner.Savepoints = c.nextWrites, c.nextJournal, c.nextSaves
				c.owner.GinRevision++
			} else {
				c.prepared.Writes, c.prepared.Catalog = c.nextWrites, c.nextJournal
				c.prepared.GinRevision++
			}
		}
		s.Stage.Rows, s.OwnWrites, s.Catalog = nextStage, nextOwn, nextJournal
		h.UnlockHeapStatisticsCohortLocked(locked)
		return storage.GinPageEstimate(incidences), nil
	}
}

func sameGinWriteMaps(a, b map[ids.ObjectAddress][]storage.RowVersion) bool {
	if len(a) != len(b) {
		return false
	}
	for address, rows := range a {
		next, ok := b[address]
		if !ok || !storage.SameVersionRoot(rows, next) {
			return false
		}
	}
	return true
}
