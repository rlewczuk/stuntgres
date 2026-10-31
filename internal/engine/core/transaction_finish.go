package core

import (
	"context"
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) Finish(err error) error {
	if s == nil || s.self != s {
		return errors.New("stuntgres: foreign or missing statement scope")
	}
	defer s.endIndexProgress(s.indexProgress)
	if s.Finished {
		return s.result
	}
	// COPY includes queued AFTER work and terminal foreign modification hooks.
	// Finish alone owns its exact-entry cleanup; the defer covers early exits
	// and panic, while the normal boundary below precedes transaction publication.
	progress := s.copyProgress
	if progress != nil {
		defer func() { s.endCopyProgress(progress) }()
	}
	if err == nil && len(s.foreignConstructions) != 0 {
		err = errors.New("stuntgres: incomplete foreign construction")
	}
	copyState := s.CopyFrom
	// Finishing still consumes held cursors, foreign-action recipes and index
	// expressions. Their borrowed admission context ends only after those users.
	defer func() {
		for _, construction := range s.foreignConstructions {
			construction.invalidate()
		}
		s.foreignConstructions = nil
		s.afterTriggers = nil
		s.currentSnapshot = nil
		s.snapshotFrontier = 0
		s.snapshotTarget = visibility.Root{}
		s.snapshotTargets = nil
		for _, program := range s.rlsPrograms {
			program.Close()
		}
		s.rlsPrograms, s.rlsActive = nil, nil
		s.RoutineAuth = AuthorizationView{}
		s.RoutineDependencies = nil
		s.routinePrograms = nil
		s.checkPrograms = nil
		s.Domains.endAdmission()
		s.copyInputRaw, s.copyInputActive = nil, nil
		s.partitionsSlot.DropPrograms()
		for i := range s.mutations {
			s.mutations[i].RoutineDependencies = nil
		}
		if s.authority != nil {
			s.authority.Host = nil
		}
	}()
	// ERROR recovery stops the budget before rollback/root cleanup. Restoring
	// a positive setting below is not an explicit assignment and cannot rearm it.
	if err != nil {
		s.Op.Session.FinishScopeTimeout(&s.lease, s.Op, s.message, s.TxRef, true)
	}
	if s.controlOutcome == controlExecuted || (s.controlOutcome == controlDescribed && err == nil) {
		// A control has no stage/view. In particular, do not advance or recheck
		if s.ReplacementRead {
			s.Engine().ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		if cleanupErr := drainCursorCleanup(s.Owner, s.Op.Context(), s); err == nil {
			err = cleanupErr
		}
		s.Owner.Mu.Lock()
		finishNotificationBoundary(s.Owner)
		s.Owner.Mu.Unlock()
		// the new incarnation installed by CHAIN after its linearization.
		s.Finished = true
		s.result = err
		s.Op.Session.FinishScopeTimeout(&s.lease, s.Op, s.message, s.TxRef, err != nil)
		s.releaseCopyReservation()
		if !s.message {
			s.Owner.Admission.Unlock()
		}
		return err
	}
	// Provider reentry and the Execute-to-Finish interval must not retain
	// prospective ALTER name claims. Acquire them at the real publication
	// boundary; the unchanged original name observations still reject a peer
	// collision after admission.
	if err == nil {
		err = s.acquireAlterCatalogNames()
	}
	t := s.Owner
	e := s.Engine()
	published := false
	t.Mu.Lock()
	// Parsing can fail before classification; admitted failures still abort
	// explicit work. Completed row/catalog/session utilities have already passed
	// their installation preflight; later cancellation cannot retract success.
	if err == nil && s.worked && (!s.Completed || s.Catalog != nil) {
		err = e.validateOperation(s.Op)
	}
	if err == nil && s.worked && s.Catalog != nil {
		t.Mu.Unlock()
		e.Mu.Lock()
		err = s.AcquireJournalRoleDependenciesLocked(s.Catalog)
		e.Mu.Unlock()
		t.Mu.Lock()
	}
	// Implicit HOLD declaration installation is not its external completion:
	// unfetched targets must settle before publication and ON COMMIT actions.
	if err == nil && s.worked && t.State == session.TxImplicit && !t.RetainsTransaction() {
		t.Mu.Unlock()
		err = s.materializeHeldCursors()
		t.Mu.Lock()
	}
	if err == nil && s.worked {
		t.Mu.Unlock()
		err = s.completeCommandEffects(t.State == session.TxImplicit && !t.RetainsTransaction(), false)
		t.Mu.Lock()
	}
	if s.foreignTablesOrZero().owner != nil {
		t.Mu.Unlock()
		err = s.finishForeignOwner(s.foreignTablesOrZero().owner, err)
		t.Mu.Lock()
	}
	t.Mu.Unlock()
	err = s.finishForeignModifications(err)
	for _, program := range s.rlsPrograms {
		err = program.FinishSources(err)
	}
	if progress != nil {
		s.endCopyProgress(progress)
		progress = nil
	}
	t.Mu.Lock()
	if err == nil && s.worked {
		catalog := t.Catalog
		if s.Catalog != nil {
			catalog = s.Catalog
		}
		if t.State == session.TxImplicit && !t.RetainsTransaction() {
			if !s.Completed || s.Catalog != nil {
				t.Mu.Unlock()
				err = e.publishRows(s.Op, t.Ref, s.Stage.Rows, s.Stage.Deletes, catalog, s.CatalogAuthority, s)
				published = err == nil
				t.Mu.Lock()
			}
		} else if !s.sessionCompleted {
			err = s.mergeSegmentLocked(catalog)
		}
		if err == nil {
			t.Command++
		}
	}
	if t.State == session.TxImplicit && err != nil && ownerHasForeign(t) {
		t.Mu.Unlock()
		s.foreignUnlocked()
		_ = disposeSQLCursors(s.Op.Context(), PayloadOf(t.Session).Cursors.endTransaction(t.Ref))
		t.Mu.Lock()
	}
	if len(s.foreignTablesOrZero().statements) != 0 {
		t.Mu.Unlock()
		err = s.finishForeignStatements(err)
		t.Mu.Lock()
	}
	if len(s.foreignTablesOrZero().holdPromotions) != 0 {
		if err != nil && !published && ownerHasForeign(t) {
			t.Mu.Unlock()
			err = s.discardForeignHoldPromotions(err)
			t.Mu.Lock()
		} else {
			s.foreignTablesSlot.clearHoldPromotions()
		}
	}
	if err != nil {
		s.Op.Session.FinishScopeTimeout(&s.lease, s.Op, s.message, s.TxRef, true)
	}
	var diagnostic *sqlstate.Error
	admittedFailure := s.worked || errors.As(err, &diagnostic) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	var endedTicket *lock.Ticket
	if t.State == session.TxImplicit && (!t.RetainsTransaction() || err != nil) {
		t.Session.Settings.FinishTransaction(err == nil)
		e.endSerializable(t.Ref, err == nil)
		endedTicket = t.Ticket
		t.Ticket = nil
		clearTransaction(t)
		t.Session.RetireTransactionTimeout()
	} else if err != nil && admittedFailure && (t.State == session.TxActive || t.State == session.TxAborted) {
		endedTicket = e.failExplicitTransactionLocked(s.Op, t, err)
	}
	e.Mu.Lock()
	e.reconcileReplicationOriginsLocked(t)
	e.Mu.Unlock()
	e.endStatementVisibility(s.TxRef, t.State != session.TxIdle && t.Isolation.Stable())
	if s.View != nil {
		s.View.Release()
		s.View = nil
	}
	s.partitionsSlot.ReleaseValidationView()
	s.Stage.Rows = nil
	s.Stage.Deletes = nil
	s.notificationActions = nil
	s.notificationEvents = nil
	s.multiEvents = nil
	s.OwnDeletes = nil
	s.OwnWrites = nil
	s.largeObjectOpening = catalog.Read{}
	s.indexesSlot.EndStatement()
	s.Inheritance.endStatement()
	s.LockTicket = nil
	s.viewCreationSlot.EndStatement()
	s.QuerySources = nil
	s.QueryLockAdmission = analyze.LockAdmission{}
	s.QueryLockContext = nil
	s.StatementMutation = StatementMutation{}
	s.mutations = nil
	s.ActiveMutation = 0
	s.cteInvocation = nil
	s.Finished = true
	s.Op.Session.FinishScopeTimeout(&s.lease, s.Op, s.message, s.TxRef, err != nil)
	s.result = err
	finishNotificationBoundary(t)
	t.Mu.Unlock()
	if s.ReplacementRead {
		e.ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
	if cleanupErr := drainCursorCleanup(t, s.Op.Context(), s); err == nil {
		err = cleanupErr
	}
	s.result = err
	if err == nil {
		copyState.Success(s.Op)
	}
	e.EndLockOwner(endedTicket)
	s.releaseCopyReservation()
	if !s.message {
		t.Admission.Unlock()
	}
	return err
}

// failExplicitTransactionLocked is the canonical explicit failed-statement
// disposal. Admission and t.Mu are held; foreign savepoint work temporarily drops
// t.Mu exactly as ordinary Finish does. The failed block retains its incarnation.
func (e *Engine) failExplicitTransactionLocked(op *session.Operation, t *session.TransactionOwner, err error) *lock.Ticket {
	defer func() {
		e.Mu.Lock()
		e.reconcileReplicationOriginsLocked(t)
		e.Mu.Unlock()
	}()
	if errors.Is(err, ids.ErrCatalogReplaced) {
		ownerExtensionOf(t).detachCursors(PayloadOf(t.Session).Cursors.clear())
		t.TruncateSavepoints(0)
	}
	if len(t.Savepoints) != 0 {
		if t.State == session.TxActive {
			mark := t.Savepoints[len(t.Savepoints)-1]
			t.Mu.Unlock()
			remoteErr := foreignSavepoint(t, context.WithoutCancel(op.Context()), 1, mark.Serial)
			t.Mu.Lock()
			if remoteErr != nil {
				ownerExtensionOf(t).retireForeign()
			}
			restoreSavepoint(t, mark, mark.Settings.Clone())
			e.Locks.Restore(t.Ticket, mark.Locks)
		}
		t.State = session.TxAborted
		return nil
	}
	ownerExtensionOf(t).retireForeign()
	ownerExtensionOf(t).largeObjects = largeObjectDescriptors{}
	ownerExtensionOf(t).largeObjectBase = nil
	t.Session.Settings.FinishTransaction(false)
	e.endSerializable(t.Ref, false)
	endedTicket := t.Abort()
	syncCharacteristics(t)
	ownerExtensionOf(t).detachCursors(PayloadOf(t.Session).Cursors.endTransaction(t.Ref))
	return endedTicket
}

// mergeSegmentLocked transfers the checked stage into the transaction owner.
// Admission and t.Mu are held; callbacks temporarily release t.Mu.
func (s *StatementScope) mergeSegmentLocked(catalog *session.Journal) error {
	t, e := s.Owner, s.Engine()
	var err error
	var deletes map[ids.ObjectAddress]*visibility.PrivateDeletes
	var writes map[ids.ObjectAddress][]storage.RowVersion
	if s.ProjectedRoots {
		writes, deletes = s.Stage.Rows, s.Stage.Deletes
	} else {
		deletes = visibility.MergeDeletions(t.Deletes, s.Stage.Deletes)
	}
	t.Mu.Unlock()
	err = s.validatePrivateIndexExpressions(catalog, deletes)
	if s.foreignTablesOrZero().owner != nil {
		err = s.finishForeignOwner(s.foreignTablesOrZero().owner, err)
	}
	t.Mu.Lock()
	if err == nil {
		if !s.ProjectedRoots {
			writes = visibility.PrepareOwnWrites(t.Writes, s.Stage.Rows, s.Stage.Replacing)
		}
		notificationActions, notificationEvents := t.MergeNotifications(s.notificationActions, s.notificationEvents)
		session := s.Op.Session
	privateForeignFence:
		e.Mu.Lock()
		err = s.validateInheritanceRelationshipsLocked(e)
		session.Mu.Lock()
		if err == nil {
			err = PublicationOperationLocked(e, s.Op)
		}
		if err == nil && t.Ref.Epoch() != e.Catalog.State.Epoch() {
			err = ids.ErrCatalogReplaced
		}
		if err == nil && s.Catalog != nil {
			err = e.CatalogPreflightLocked(catalog)
		}
		if err == nil {
			err = e.catalogAuthorityPreflightLocked(s.CatalogAuthority, catalog)
		}
		if err == nil && len(s.foreignTablesOrZero().statements) != 0 {
			session.Mu.Unlock()
			e.Mu.Unlock()
			t.Mu.Unlock()
			settleErr := s.finishForeignStatements(nil)
			t.Mu.Lock()
			if settleErr == nil {
				goto privateForeignFence
			}
			err = settleErr
			e.Mu.Lock()
			session.Mu.Lock()
		}
		if err == nil {
			for a, rows := range s.Stage.Rows {
				if !s.Stage.Replacing && len(t.Writes[a]) != 0 {
					copy(writes[a][len(t.Writes[a]):], rows)
				}
			}
			t.Writes = writes
			t.Deletes = deletes
			t.Catalog = catalog
			t.NotificationActions = notificationActions
			t.NotificationEvents = notificationEvents
			if s.IndexesOrZero().ConstraintsStarted {
				ownerExtensionOf(t).constraintModes = s.IndexesOrZero().ConstraintModes
			}
			e.reconcileSerializableWritesLocked(t)
		}
		session.Mu.Unlock()
		e.Mu.Unlock()
	}
	return err
}
