package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) Finish(err error) error {
	if s == nil || s.self != s {
		return errors.New("stuntgres: foreign or missing statement scope")
	}
	if s.finished {
		return s.result
	}
	// Finishing still consumes held cursors, foreign-action recipes and index
	// expressions. Their borrowed admission context ends only after those users.
	defer func() {
		s.routineAuth = AuthorizationView{}
		s.routineDependencies = nil
		s.routinePrograms = nil
		s.domains.endAdmission()
		s.partitionsSlot.dropPrograms()
		for i := range s.mutations {
			s.mutations[i].routineDependencies = nil
		}
		if s.authority != nil {
			s.authority.Host = nil
		}
	}()
	// ERROR recovery stops the budget before rollback/root cleanup. Restoring
	// a positive setting below is not an explicit assignment and cannot rearm it.
	if err != nil {
		s.op.Session.FinishScopeTimeout(&s.lease, s.op, s.message, s.txRef, true)
	}
	if s.controlOutcome == controlExecuted || (s.controlOutcome == controlDescribed && err == nil) {
		// A control has no stage/view. In particular, do not advance or recheck
		if s.replacementRead {
			s.engine().replacementMu.RUnlock()
			s.replacementRead = false
		}
		if cleanupErr := drainCursorCleanup(s.owner, s.op.Context(), s); err == nil {
			err = cleanupErr
		}
		s.owner.Mu.Lock()
		finishNotificationBoundary(s.owner)
		s.owner.Mu.Unlock()
		// the new incarnation installed by CHAIN after its linearization.
		s.finished = true
		s.result = err
		s.op.Session.FinishScopeTimeout(&s.lease, s.op, s.message, s.txRef, err != nil)
		s.releaseCopyReservation()
		if !s.message {
			s.owner.Admission.Unlock()
		}
		return err
	}
	t := s.owner
	e := s.engine()
	published := false
	t.Mu.Lock()
	// Parsing can fail before classification; admitted failures still abort
	// explicit work. Completed row/catalog/session utilities have already passed
	// their installation preflight; later cancellation cannot retract success.
	if err == nil && s.worked && !s.completed {
		err = e.validateOperation(s.op)
	}
	// Implicit HOLD declaration installation is not its external completion:
	// unfetched targets must settle before publication and ON COMMIT actions.
	if err == nil && s.worked && t.State == TxImplicit && !t.RetainsTransaction() {
		t.Mu.Unlock()
		err = s.materializeHeldCursors()
		t.Mu.Lock()
	}
	if err == nil && s.worked {
		t.Mu.Unlock()
		err = s.checkExclusionEvents(t.State == TxImplicit && !t.RetainsTransaction(), nil)
		if err == nil {
			err = s.settleForeignActions()
		}
		if err == nil && s.foreignActionsOrZero().exclusionAdded {
			err = s.checkExclusionEvents(t.State == TxImplicit && !t.RetainsTransaction(), nil)
		}
		if err == nil {
			err = s.checkForeignKeys()
		}
		t.Mu.Lock()
	}
	if s.foreignTablesOrZero().owner != nil {
		t.Mu.Unlock()
		err = s.finishForeignOwner(s.foreignTablesOrZero().owner, err)
		t.Mu.Lock()
	}
	t.Mu.Unlock()
	err = s.finishForeignModifications(err)
	t.Mu.Lock()
	if err == nil && s.worked {
		catalog := t.Catalog
		if s.catalog != nil {
			catalog = s.catalog
		}
		if t.State == TxImplicit && !t.RetainsTransaction() {
			if !s.completed {
				t.Mu.Unlock()
				err = e.publishRows(s.op, t.Ref, s.stage.rows, s.stage.deletes, catalog, s.catalogAuthority, s)
				published = err == nil
				t.Mu.Lock()
			}
		} else if !s.sessionCompleted {
			var deletes map[ids.ObjectAddress]*visibility.PrivateDeletes
			var writes map[ids.ObjectAddress][]storage.RowVersion
			if s.projectedRoots {
				writes, deletes = s.stage.rows, s.stage.deletes
			} else {
				deletes = mergeDeletions(t.Deletes, s.stage.deletes)
			}
			t.Mu.Unlock()
			err = s.validatePrivateIndexExpressions(catalog, deletes)
			if s.foreignTablesOrZero().owner != nil {
				err = s.finishForeignOwner(s.foreignTablesOrZero().owner, err)
			}
			t.Mu.Lock()
			if err == nil {
				if !s.projectedRoots {
					writes = prepareOwnWrites(t.Writes, s.stage.rows, s.stage.replacing)
				}
				notificationActions := t.NotificationActions
				if len(notificationActions) == 0 {
					notificationActions = s.notificationActions
				} else {
					notificationActions = append(notificationActions, s.notificationActions...)
				}
				notificationEvents := appendNotificationEvents(t.NotificationEvents, s.notificationEvents)
				session := s.op.Session
			privateForeignFence:
				e.mu.Lock()
				session.Mu.Lock()
				err = publicationOperationLocked(e, s.op)
				if err == nil && t.Ref.Epoch() != e.catalog.state.Epoch() {
					err = ids.ErrCatalogReplaced
				}
				if err == nil && s.catalog != nil {
					err = e.catalogPreflightLocked(catalog)
				}
				if err == nil {
					err = e.catalogAuthorityPreflightLocked(s.catalogAuthority, catalog)
				}
				if err == nil && len(s.foreignTablesOrZero().statements) != 0 {
					session.Mu.Unlock()
					e.mu.Unlock()
					t.Mu.Unlock()
					settleErr := s.finishForeignStatements(nil)
					t.Mu.Lock()
					if settleErr == nil {
						goto privateForeignFence
					}
					err = settleErr
					e.mu.Lock()
					session.Mu.Lock()
				}
				if err == nil {
					for a, rows := range s.stage.rows {
						if !s.stage.replacing && len(t.Writes[a]) != 0 {
							copy(writes[a][len(t.Writes[a]):], rows)
						}
					}
					t.Writes = writes
					t.Deletes = deletes
					t.Catalog = catalog
					t.NotificationActions = notificationActions
					t.NotificationEvents = notificationEvents
					if s.indexesOrZero().exclusionStarted {
						ownerExtensionOf(t).exclusionModes = s.indexesOrZero().exclusionModes
					}
					e.reconcileSerializableWritesLocked(t)
				}
				session.Mu.Unlock()
				e.mu.Unlock()
			}
		}
		if err == nil {
			t.Command++
		}
	}
	if t.State == TxImplicit && err != nil && len(ownerExtensionOrZero(t).foreign.participants) != 0 {
		t.Mu.Unlock()
		s.foreignUnlocked()
		_ = disposeSQLCursors(s.op.Context(), payloadOf(t.Session).cursors.endTransaction(t.Ref))
		t.Mu.Lock()
	}
	if len(s.foreignTablesOrZero().statements) != 0 {
		t.Mu.Unlock()
		err = s.finishForeignStatements(err)
		t.Mu.Lock()
	}
	if len(s.foreignTablesOrZero().holdPromotions) != 0 {
		if err != nil && !published && len(ownerExtensionOrZero(t).foreign.participants) != 0 {
			t.Mu.Unlock()
			err = s.discardForeignHoldPromotions(err)
			t.Mu.Lock()
		} else {
			s.foreignTablesSlot.clearHoldPromotions()
		}
	}
	if err != nil {
		s.op.Session.FinishScopeTimeout(&s.lease, s.op, s.message, s.txRef, true)
	}
	var diagnostic *sqlstate.Error
	admittedFailure := s.worked || errors.As(err, &diagnostic) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	var endedTicket *lock.Ticket
	if t.State == TxImplicit && (!t.RetainsTransaction() || err != nil) {
		t.Session.Settings.FinishTransaction(err == nil)
		e.endSerializable(t.Ref, err == nil)
		endedTicket = t.Ticket
		t.Ticket = nil
		clearTransaction(t)
		t.Session.RetireTransactionTimeout()
	} else if err != nil && admittedFailure && (t.State == TxActive || t.State == TxAborted) {
		if errors.Is(err, ids.ErrCatalogReplaced) {
			ownerExtensionOf(t).detachCursors(payloadOf(t.Session).cursors.clear())
			t.TruncateSavepoints(0)
		}
		if len(t.Savepoints) != 0 {
			if t.State == TxActive {
				mark := t.Savepoints[len(t.Savepoints)-1]
				t.Mu.Unlock()
				remoteErr := foreignSavepoint(t, context.WithoutCancel(s.op.Context()), 1, mark.Serial)
				t.Mu.Lock()
				if remoteErr != nil {
					ownerExtensionOf(t).retireForeign()
				}
				restoreSavepoint(t, mark, mark.Settings.Clone())
				e.locks.Restore(t.Ticket, mark.Locks)
			}
			t.State = TxAborted
		} else {
			ownerExtensionOf(t).retireForeign()
			t.Session.Settings.FinishTransaction(false)
			e.endSerializable(t.Ref, false)
			t.Isolation, t.ReadOnly = t.EntryIsolation, t.EntryReadOnly
			t.Deferrable = t.EntryDeferrable
			t.State = TxAborted
			syncCharacteristics(t)
			t.Writes = nil
			t.Deletes = nil
			t.Roots = nil
			t.SequenceCatalog = nil
			t.Frontier = 0
			t.Catalog = nil
			t.NotificationActions = nil
			t.NotificationEvents = nil
			endedTicket = t.Ticket
			t.Ticket = nil
			ownerExtensionOf(t).detachCursors(payloadOf(t.Session).cursors.endTransaction(t.Ref))
		}
	}
	e.endStatementVisibility(s.txRef, t.State != TxIdle && stableIsolation(t.Isolation))
	if s.view != nil {
		s.view.Release()
		s.view = nil
	}
	s.partitionsSlot.releaseValidationView()
	s.stage.rows = nil
	s.stage.deletes = nil
	s.notificationActions = nil
	s.notificationEvents = nil
	s.multiEvents = nil
	s.ownDeletes = nil
	s.ownWrites = nil
	s.indexesSlot.endStatement()
	s.inheritance.endStatement()
	s.ticket = nil
	s.viewCreationSlot.endStatement()
	s.querySources = nil
	s.queryLockAdmission = analyze.LockAdmission{}
	s.queryLocks = nil
	s.statementMutation = statementMutation{}
	s.mutations = nil
	s.activeMutation = 0
	s.cteInvocation = nil
	s.finished = true
	s.op.Session.FinishScopeTimeout(&s.lease, s.op, s.message, s.txRef, err != nil)
	s.result = err
	finishNotificationBoundary(t)
	t.Mu.Unlock()
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	if cleanupErr := drainCursorCleanup(t, s.op.Context(), s); err == nil {
		err = cleanupErr
	}
	s.result = err
	e.endLockOwner(endedTicket)
	s.releaseCopyReservation()
	if !s.message {
		t.Admission.Unlock()
	}
	return err
}
