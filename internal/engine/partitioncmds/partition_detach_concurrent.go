package partitioncmds

import (
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Entered with Engine.Mu and the parent SUE admission; consumes Engine.Mu.
func detachConcurrentLocked(e *core.Engine, s *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTableDetachPartition, parent *catalog.Relation) (core.Result, error) {
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	if parent.Partition == nil {
		err := partition.DetachKindError(auth.Catalog, parent, false)
		if raw.Mode == parser.DetachFinalize {
			d := *err.(*sqlstate.Error)
			d.Message = strings.Replace(d.Message, "DETACH PARTITION", "DETACH PARTITION ... FINALIZE", 1)
			err = &d
		}
		return fail(err)
	}
	if err := partitionAttachName(e, auth, raw.Name, s.LockedRef); err != nil {
		return fail(err)
	}
	if raw.Mode == parser.DetachConcurrent {
		if parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
			return fail(core.SQLError("55000", "cannot detach partitions concurrently when a default partition exists"))
		}
		if pending := partition.PendingChild(auth.Catalog, parent); pending != nil {
			return fail(partition.ConcurrentPendingError(auth.Catalog, parent, pending))
		}
	}
	childRef, err := e.ResolveRelationReference(auth, raw.Partition, true)
	if err != nil {
		return fail(err)
	}
	child := auth.Catalog.MustRelation(childRef.Address)
	if child.RelationKind == catalog.IndexRelation || child.RelationKind == catalog.CompositeRelation {
		return fail(partition.DetachKindError(auth.Catalog, child, true))
	}
	if err = partition.DetachMembership(parent, child); err != nil {
		return fail(err)
	}
	if raw.Mode == parser.DetachFinalize {
		if err = partition.RequirePending(parent, child); err != nil {
			return fail(err)
		}
	}
	if err = s.ObservePartitionRelationLocked(e, childRef); err != nil {
		return fail(err)
	}
	e.Mu.Unlock()
	mode := lock.AccessExclusive
	if raw.Mode == parser.DetachConcurrent {
		mode = lock.ShareUpdateExclusive
	}
	if err = s.AcquirePartitionLock(childRef, mode); err != nil {
		return core.Result{}, err
	}
	if err = s.AcquirePartitionDescendants(childRef, mode); err != nil {
		return core.Result{}, err
	}
	if raw.Mode == parser.DetachConcurrent {
		if err = publishDetachPhase(e, s, raw, childRef, true, false); err != nil {
			return core.Result{}, err
		}
		e.Mu.Lock()
		e.Visibility.ResetPhaseLocked(s.TxRef)
		// First-phase descriptors are no longer operation authority. Subsequent
		// admissions resolve the same refs against the committed pending graph.
		phaseRefs := make([]ids.ObjectRef, 0, len(s.PartitionRelations))
		for a := range s.PartitionRelations {
			if a != s.LockedRef.Address {
				phaseRefs = append(phaseRefs, e.Catalog.Domain.Ref(auth.Catalog.Epoch(), a))
			}
		}
		s.PartitionRelations = nil
		e.Mu.Unlock()
		cohort, err := e.Locks.RetireDetachPhase(s.LockTicket, s.LockedRef, phaseRefs)
		if err != nil {
			return core.Result{}, err
		}
		for _, owner := range cohort {
			if err = s.WriteHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
				return core.Result{}, err
			}
		}
		if err = s.AcquirePartitionLock(s.LockedRef, lock.ShareUpdateExclusive); err != nil {
			return core.Result{}, err
		}
		if err = s.AcquirePartitionLock(childRef, lock.AccessExclusive); err != nil {
			return core.Result{}, err
		}
		if err = s.AcquirePartitionDescendants(childRef, lock.AccessExclusive); err != nil {
			return core.Result{}, err
		}
	} else {
		if err = waitDetachSnapshots(e, s); err != nil {
			return core.Result{}, err
		}
	}
	if err = publishDetachPhase(e, s, raw, childRef, false, raw.Mode == parser.DetachFinalize); err != nil {
		return core.Result{}, err
	}
	if raw.Mode == parser.DetachConcurrent {
		s.CompleteUtility()
	} else {
		s.RecordDDLCommand("ALTER TABLE", parent.Address())
	}
	return core.Result{}, nil
}

func detachPhaseAuthorityLocked(e *core.Engine, s *core.StatementScope, raw *parser.AlterTableDetachPartition, childRef ids.ObjectRef, reopen bool) (core.AuthorizationView, *catalog.Relation, *catalog.Relation, error) {
	auth, err := e.StatementAuthorizationLocked(s.Op, raw)
	if err != nil {
		return auth, nil, nil, err
	}
	parent, ok := auth.Catalog.Relation(s.LockedRef.Address)
	if !ok || parent.Partition == nil || s.LockedRef.Epoch != auth.Catalog.Epoch() {
		return auth, nil, nil, ids.ErrCatalogReplaced
	}
	child, ok := auth.Catalog.Relation(childRef.Address)
	if !ok || childRef.Epoch != auth.Catalog.Epoch() {
		return auth, nil, nil, ids.ErrCatalogReplaced
	}
	if reopen {
		// Independent phases reopen saved OIDs, not old spellings. A legal rename
		// or namespace transfer cannot retarget the old name to a replacement.
		s.LockedDescriptor = parent
	} else {
		err = partitionAttachName(e, auth, raw.Name, s.LockedRef)
		if err == nil {
			err = partitionAttachName(e, auth, raw.Partition, childRef)
		}
	}
	if err == nil {
		err = s.ValidateLockedRelation(e)
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: s.LockedRef, OwnerOnly: true, OwnerNoun: "table"})
	}
	return auth, parent, child, err
}

func publishDetachPhase(e *core.Engine, s *core.StatementScope, raw *parser.AlterTableDetachPartition, childRef ids.ObjectRef, pending, transactional bool) error {
	e.Mu.Lock()
	auth, parent, child, err := detachPhaseAuthorityLocked(e, s, raw, childRef, !pending && !transactional)
	if err == nil {
		if pending {
			err = partition.DetachMembership(parent, child)
		} else {
			err = partition.RequirePending(parent, child)
		}
	}
	if err == nil && pending {
		if parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
			err = core.SQLError("55000", "cannot detach partitions concurrently when a default partition exists")
		} else if other := partition.PendingChild(auth.Catalog, parent); other != nil {
			err = partition.ConcurrentPendingError(auth.Catalog, parent, other)
		}
	}
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, s.Op.Session)
	stage.Indexes().Output = s.Output
	for a := range s.PartitionRelations {
		stage.DomainBases = append(stage.DomainBases, auth.Catalog.Get(a))
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if pending {
		child, err = stageDetachBound(e, s, auth, stage, parent, child)
		if err == nil {
			e.Mu.Lock()
			revision, failure := e.Catalog.RevisionLocked()
			e.Mu.Unlock()
			if failure != nil {
				err = failure
			} else {
				err = partition.StagePending(stage, parent, child, stage.Base.MustRelation(childRef.Address), revision)
			}
		}
	} else {
		err = partition.StageDetach(stage, stage.Base, parent, child)
	}
	if err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(s.Op.Context(), stage)
	if err != nil {
		return err
	}
	if transactional {
		return e.StageCatalogOverlay(s, stage, s.PendingCatalog().Clone())
	}
	_, err = e.PublishCatalogOperation(s.Op, s, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	return err
}

func waitDetachSnapshots(e *core.Engine, s *core.StatementScope) error {
	e.Mu.Lock()
	xmin, err := e.VisibilitySnapshotLocked(s.TxRef, false)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	// FINALIZE is transactional: its owner's stable snapshot must stay advertised
	// until normal transaction retirement. OlderSnapshotsLocked excludes self.
	cohort := e.Visibility.OlderSnapshotsLocked(s.TxRef, xmin)
	e.Mu.Unlock()
	for _, ref := range cohort {
		e.Mu.Lock()
		eligible := e.Visibility.SnapshotOlderLocked(ref, xmin)
		e.Mu.Unlock()
		if eligible {
			if owner := e.Locks.TransactionTicket(ref); owner != nil {
				if err := s.WriteHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
