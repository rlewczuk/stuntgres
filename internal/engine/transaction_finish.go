package engine

import (
	"context"
	"errors"

	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) Finish(err error) error {
	if s == nil || s.self != s {
		return errors.New("stuntgres: foreign or missing statement scope")
	}
	if s.finished {
		return s.result
	}
	if s.authority != nil {
		s.authority.scope = nil
	}
	// ERROR recovery stops the budget before rollback/root cleanup. Restoring
	// a positive setting below is not an explicit assignment and cannot rearm it.
	if err != nil {
		s.op.session.finishScopeTimeout(s, true)
	}
	if s.controlOutcome == controlExecuted || (s.controlOutcome == controlDescribed && err == nil) {
		// A control has no stage/view. In particular, do not advance or recheck
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
	t.mu.Lock()
	// Parsing can fail before classification; admitted failures still abort
	// explicit work. Completed row/catalog/session utilities have already passed
	// their installation preflight; later cancellation cannot retract success.
	if err == nil && s.worked && !s.completed {
		err = e.validateOperation(s.op)
	}
	// Implicit HOLD declaration installation is not its external completion:
	// unfetched targets must settle before publication and ON COMMIT actions.
	if err == nil && s.worked && t.state == TxImplicit && !t.retainedMessage {
		t.mu.Unlock()
		err = s.materializeHeldCursors()
		t.mu.Lock()
	}
	if err == nil && s.worked {
		t.mu.Unlock()
		err = s.checkExclusionEvents(t.state == TxImplicit && !t.retainedMessage, nil)
		if err == nil {
			err = s.settleForeignActions()
		}
		if err == nil && s.foreignActionExclusionAdded {
			err = s.checkExclusionEvents(t.state == TxImplicit && !t.retainedMessage, nil)
		}
		if err == nil {
			err = s.checkForeignKeys()
		}
		t.mu.Lock()
	}
	if err == nil && s.worked {
		catalog := t.catalog
		if s.catalog != nil {
			catalog = s.catalog
		}
		if t.state == TxImplicit && !t.retainedMessage {
			if !s.completed {
				t.mu.Unlock()
				err = e.publishRows(s.op, t.ref, s.stage.rows, s.stage.deletes, catalog, s.catalogAuthority)
				t.mu.Lock()
			}
		} else if !s.sessionCompleted {
			var deletes map[ObjectAddress]*privateDeletionRoot
			var writes map[ObjectAddress][]rowVersion
			if s.projectedRoots {
				writes, deletes = s.stage.rows, s.stage.deletes
			} else {
				deletes = mergeDeletions(t.deletes, s.stage.deletes)
			}
			t.mu.Unlock()
			err = s.validatePrivateIndexExpressions(catalog, deletes)
			t.mu.Lock()
			if err == nil {
				if !s.projectedRoots {
					writes = prepareOwnWrites(t.writes, s.stage.rows, s.stage.replacing)
				}
				session := s.op.session
				e.mu.Lock()
				session.mu.Lock()
				err = publicationOperationLocked(e, s.op)
				if err == nil && t.ref.epoch != e.state.epoch {
					err = ErrCatalogReplaced
				}
				if err == nil && s.catalog != nil {
					err = e.catalogPreflightLocked(catalog)
				}
				if err == nil {
					err = e.catalogAuthorityPreflightLocked(s.catalogAuthority, catalog)
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
					if s.exclusionStarted {
						t.exclusionModes = s.exclusionModes
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
	if err != nil {
		s.op.session.finishScopeTimeout(s, true)
	}
	var diagnostic *sqlstate.Error
	admittedFailure := s.worked || errors.As(err, &diagnostic) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	var endedTicket *relationTicket
	if t.state == TxImplicit && (!t.retainedMessage || err != nil) {
		t.session.settings.finishTransaction(err == nil)
		e.endSerializable(t.ref, err == nil)
		endedTicket = t.ticket
		t.ticket = nil
		t.clear()
		t.session.retireTransactionTimeout()
	} else if err != nil && admittedFailure && (t.state == TxActive || t.state == TxAborted) {
		if errors.Is(err, ErrCatalogReplaced) {
			t.session.cursors.clear()
			t.truncateSavepoints(0)
		}
		if len(t.savepoints) != 0 {
			if t.state == TxActive {
				mark := t.savepoints[len(t.savepoints)-1]
				t.restoreSavepoint(mark, mark.settings.clone())
				e.locks.restore(t.ticket, mark.locks)
			}
			t.state = TxAborted
		} else {
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
			endedTicket = t.ticket
			t.ticket = nil
			t.session.cursors.endTransaction(t.ref)
		}
	}
	e.endStatementVisibility(s.txRef, t.state != TxIdle && stableIsolation(t.isolation))
	if s.view != nil {
		s.view.Release()
		s.view = nil
	}
	s.stage.rows = nil
	s.stage.deletes = nil
	s.ownDeletes = nil
	s.ownWrites = nil
	s.uniqueBinding = nil
	s.insertConflict = nil
	s.partitionInsert = nil
	s.rlsTargetDenied = false
	s.rlsProposal, s.rlsMergeInsert, s.rlsMergeUpdate = rlsUnrestricted, rlsUnrestricted, rlsUnrestricted
	s.rlsProposalTable = ""
	s.partitionRelations = nil
	s.inheritanceRelations = nil
	s.inheritanceCatalog = CatalogRead{}
	s.ticket = nil
	s.lockedDescriptor = nil
	s.viewWrite = nil
	s.indexMaintenance = nil
	s.viewCreationNamespace = ObjectRef{}
	s.viewPersistence = ""
	s.viewBinding = binding{}
	s.viewChanges = nil
	s.lockedStorage = nil
	s.querySources = nil
	s.queryLockAdmission = queryLockAdmission{}
	s.queryLocks = nil
	s.lockedIdentityOnly = false
	s.finished = true
	s.op.session.finishScopeTimeout(s, err != nil)
	s.result = err
	t.mu.Unlock()
	if s.replacementRead {
		e.replacementMu.RUnlock()
		s.replacementRead = false
	}
	e.locks.end(endedTicket)
	s.releaseCopyReservation()
	if !s.message {
		t.admission.Unlock()
	}
	return err
}
