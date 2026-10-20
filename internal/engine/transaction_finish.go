package engine

import (
	"context"
	"errors"

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
			s.authority.host = nil
		}
	}()
	// ERROR recovery stops the budget before rollback/root cleanup. Restoring
	// a positive setting below is not an explicit assignment and cannot rearm it.
	if err != nil {
		s.op.session.finishScopeTimeout(s, true)
	}
	if s.controlOutcome == controlExecuted || (s.controlOutcome == controlDescribed && err == nil) {
		// A control has no stage/view. In particular, do not advance or recheck
		if s.replacementRead {
			s.op.session.Engine().replacementMu.RUnlock()
			s.replacementRead = false
		}
		if cleanupErr := s.owner.drainCursorCleanup(s.op.Context(), s); err == nil {
			err = cleanupErr
		}
		s.owner.mu.Lock()
		s.owner.finishNotificationBoundary()
		s.owner.mu.Unlock()
		// the new incarnation installed by CHAIN after its linearization.
		s.finished = true
		s.result = err
		s.op.session.finishScopeTimeout(s, err != nil)
		s.releaseCopyReservation()
		if !s.message {
			s.owner.admission.Unlock()
		}
		return err
	}
	t := s.owner
	e := s.op.session.Engine()
	published := false
	t.mu.Lock()
	// Parsing can fail before classification; admitted failures still abort
	// explicit work. Completed row/catalog/session utilities have already passed
	// their installation preflight; later cancellation cannot retract success.
	if err == nil && s.worked && !s.completed {
		err = e.validateOperation(s.op)
	}
	// Implicit HOLD declaration installation is not its external completion:
	// unfetched targets must settle before publication and ON COMMIT actions.
	if err == nil && s.worked && t.state == TxImplicit && !t.retainsTransaction() {
		t.mu.Unlock()
		err = s.materializeHeldCursors()
		t.mu.Lock()
	}
	if err == nil && s.worked {
		t.mu.Unlock()
		err = s.checkExclusionEvents(t.state == TxImplicit && !t.retainsTransaction(), nil)
		if err == nil {
			err = s.settleForeignActions()
		}
		if err == nil && s.foreignActionsOrZero().exclusionAdded {
			err = s.checkExclusionEvents(t.state == TxImplicit && !t.retainsTransaction(), nil)
		}
		if err == nil {
			err = s.checkForeignKeys()
		}
		t.mu.Lock()
	}
	if s.foreignTablesOrZero().owner != nil {
		t.mu.Unlock()
		err = s.finishForeignOwner(s.foreignTablesOrZero().owner, err)
		t.mu.Lock()
	}
	t.mu.Unlock()
	err = s.finishForeignModifications(err)
	t.mu.Lock()
	if err == nil && s.worked {
		catalog := t.catalog
		if s.catalog != nil {
			catalog = s.catalog
		}
		if t.state == TxImplicit && !t.retainsTransaction() {
			if !s.completed {
				t.mu.Unlock()
				err = e.publishRows(s.op, t.ref, s.stage.rows, s.stage.deletes, catalog, s.catalogAuthority, s)
				published = err == nil
				t.mu.Lock()
			}
		} else if !s.sessionCompleted {
			var deletes map[ids.ObjectAddress]*visibility.PrivateDeletes
			var writes map[ids.ObjectAddress][]storage.RowVersion
			if s.projectedRoots {
				writes, deletes = s.stage.rows, s.stage.deletes
			} else {
				deletes = mergeDeletions(t.deletes, s.stage.deletes)
			}
			t.mu.Unlock()
			err = s.validatePrivateIndexExpressions(catalog, deletes)
			if s.foreignTablesOrZero().owner != nil {
				err = s.finishForeignOwner(s.foreignTablesOrZero().owner, err)
			}
			t.mu.Lock()
			if err == nil {
				if !s.projectedRoots {
					writes = prepareOwnWrites(t.writes, s.stage.rows, s.stage.replacing)
				}
				notificationActions := t.notificationActions
				if len(notificationActions) == 0 {
					notificationActions = s.notificationActions
				} else {
					notificationActions = append(notificationActions, s.notificationActions...)
				}
				notificationEvents := appendNotificationEvents(t.notificationEvents, s.notificationEvents)
				session := s.op.session
			privateForeignFence:
				e.mu.Lock()
				session.mu.Lock()
				err = publicationOperationLocked(e, s.op)
				if err == nil && t.ref.Epoch() != e.catalog.state.Epoch() {
					err = ids.ErrCatalogReplaced
				}
				if err == nil && s.catalog != nil {
					err = e.catalogPreflightLocked(catalog)
				}
				if err == nil {
					err = e.catalogAuthorityPreflightLocked(s.catalogAuthority, catalog)
				}
				if err == nil && len(s.foreignTablesOrZero().statements) != 0 {
					session.mu.Unlock()
					e.mu.Unlock()
					t.mu.Unlock()
					settleErr := s.finishForeignStatements(nil)
					t.mu.Lock()
					if settleErr == nil {
						goto privateForeignFence
					}
					err = settleErr
					e.mu.Lock()
					session.mu.Lock()
				}
				if err == nil {
					for a, rows := range s.stage.rows {
						if !s.stage.replacing && len(t.writes[a]) != 0 {
							copy(writes[a][len(t.writes[a]):], rows)
						}
					}
					t.writes = writes
					t.deletes = deletes
					t.catalog = catalog
					t.notificationActions = notificationActions
					t.notificationEvents = notificationEvents
					if s.indexesOrZero().exclusionStarted {
						t.exclusionModes = s.indexesOrZero().exclusionModes
					}
					e.reconcileSerializableWritesLocked(t)
				}
				session.mu.Unlock()
				e.mu.Unlock()
			}
		}
		if err == nil {
			t.command++
		}
	}
	if t.state == TxImplicit && err != nil && len(t.foreign.participants) != 0 {
		t.mu.Unlock()
		s.foreignUnlocked()
		_ = disposeSQLCursors(s.op.Context(), t.session.cursors.endTransaction(t.ref))
		t.mu.Lock()
	}
	if len(s.foreignTablesOrZero().statements) != 0 {
		t.mu.Unlock()
		err = s.finishForeignStatements(err)
		t.mu.Lock()
	}
	if len(s.foreignTablesOrZero().holdPromotions) != 0 {
		if err != nil && !published && len(t.foreign.participants) != 0 {
			t.mu.Unlock()
			err = s.discardForeignHoldPromotions(err)
			t.mu.Lock()
		} else {
			s.foreignTablesSlot.clearHoldPromotions()
		}
	}
	if err != nil {
		s.op.session.finishScopeTimeout(s, true)
	}
	var diagnostic *sqlstate.Error
	admittedFailure := s.worked || errors.As(err, &diagnostic) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	var endedTicket *lock.Ticket
	if t.state == TxImplicit && (!t.retainsTransaction() || err != nil) {
		t.session.settings.finishTransaction(err == nil)
		e.endSerializable(t.ref, err == nil)
		endedTicket = t.ticket
		t.ticket = nil
		t.clear()
		t.session.retireTransactionTimeout()
	} else if err != nil && admittedFailure && (t.state == TxActive || t.state == TxAborted) {
		if errors.Is(err, ids.ErrCatalogReplaced) {
			t.cursorCleanup = append(t.cursorCleanup, t.session.cursors.clear()...)
			t.truncateSavepoints(0)
		}
		if len(t.savepoints) != 0 {
			if t.state == TxActive {
				mark := t.savepoints[len(t.savepoints)-1]
				t.mu.Unlock()
				remoteErr := t.foreignSavepoint(context.WithoutCancel(s.op.Context()), 1, mark.foreignID)
				t.mu.Lock()
				if remoteErr != nil {
					t.foreign.cleanup = append(t.foreign.cleanup, t.foreign.participants...)
					t.foreign.participants = nil
				}
				t.restoreSavepoint(mark, mark.settings.clone())
				e.locks.Restore(t.ticket, mark.locks)
			}
			t.state = TxAborted
		} else {
			t.foreign.cleanup = append(t.foreign.cleanup, t.foreign.participants...)
			t.foreign.participants = nil
			t.session.settings.finishTransaction(false)
			e.endSerializable(t.ref, false)
			t.isolation, t.readOnly = t.entryIsolation, t.entryReadOnly
			t.deferrable = t.entryDeferrable
			t.state = TxAborted
			t.syncCharacteristics()
			t.writes = nil
			t.deletes = nil
			t.roots = nil
			t.sequenceCatalog = nil
			t.frontier = 0
			t.catalog = nil
			t.notificationActions = nil
			t.notificationEvents = nil
			endedTicket = t.ticket
			t.ticket = nil
			t.cursorCleanup = append(t.cursorCleanup, t.session.cursors.endTransaction(t.ref)...)
		}
	}
	e.endStatementVisibility(s.txRef, t.state != TxIdle && stableIsolation(t.isolation))
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
	s.queryLockAdmission = queryLockAdmission{}
	s.queryLocks = nil
	s.statementMutation = statementMutation{}
	s.mutations = nil
	s.activeMutation = 0
	s.cteInvocation = nil
	s.finished = true
	s.op.session.finishScopeTimeout(s, err != nil)
	s.result = err
	t.finishNotificationBoundary()
	t.mu.Unlock()
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	if cleanupErr := t.drainCursorCleanup(s.op.Context(), s); err == nil {
		err = cleanupErr
	}
	s.result = err
	e.endLockOwner(endedTicket)
	s.releaseCopyReservation()
	if !s.message {
		t.admission.Unlock()
	}
	return err
}
