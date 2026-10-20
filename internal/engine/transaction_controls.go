package engine

import (
	"errors"
	"fmt"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type controlOutcome uint8

const (
	controlOrdinary controlOutcome = iota
	controlDescribed
	controlExecuted
)

// Membership is transaction admission authority, not numeric/session identity.
// Context remains a separate check: an accepted Go commit canceled after
// admission must retain its existing failed-end/discard disposition.
func (e *Engine) transactionAuthority(op *Operation) error {
	if op == nil || op.Self != op || op.Session == nil || op.Session.SessionToken.Domain() != domainOf(e) {
		return errors.New("stuntgres: foreign or missing operation")
	}
	session := op.Session
	session.Mu.Lock()
	defer session.Mu.Unlock()
	if op.Released || session.State != live {
		if session.Timeouts.Fatal != nil {
			copy := *session.Timeouts.Fatal
			return &copy
		}
		return errors.New("stuntgres: inactive operation")
	}
	if _, ok := session.Operations[op]; !ok {
		return errors.New("stuntgres: inactive operation")
	}
	return nil
}

// Entry admission checks the caller context only after structural membership.
// Begin, Rollback and statement admission reject cancellation without mutation.
// Native Commit deliberately admits cancellation to dispose its valid old
// incarnation, preserving the established failed-commit contract.
func (e *Engine) transactionAdmission(op *Operation) error {
	if err := e.transactionAuthority(op); err != nil {
		return err
	}
	return op.Context().Err()
}

// Admission is held by the Go entrypoint or the SQL scope, never recursively.
func (e *Engine) beginAdmitted(op *Operation, t *transactionOwner, start time.Time) (ids.TxRef, error) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := e.transactionAdmission(op); err != nil {
		return ids.TxRef{}, err
	}
	if t.State != TxIdle {
		return ids.TxRef{}, errors.New("stuntgres: transaction already active")
	}
	if err := e.beginLocked(op, t, TxActive, start); err != nil {
		return ids.TxRef{}, err
	}
	ref := t.Ref
	t.Mu.Unlock()
	ticket, err := e.registerTransactionLocks(ref)
	t.Mu.Lock()
	if err != nil {
		clearTransaction(t)
		t.Session.RetireTransactionTimeout()
		return ids.TxRef{}, err
	}
	t.Ticket = ticket
	return ref, nil
}

// The owner lock precedes Engine.mu -> Session.mu. Rollback deliberately does
// not validate the old epoch, so native replacement always permits recovery.
func (e *Engine) endPreflight(op *Operation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	return publicationOperationLocked(e, op)
}

func (e *Engine) endAdmitted(op *Operation, t *transactionOwner, ref ids.TxRef, commit, chain, sqlEnd bool, nextStart time.Time, scope *StatementScope) (EndResult, error) {
	// Registration can fail. A short replacement fence keeps the prospective
	// ticket's epoch current through installation, without a lifetime fence.
	chainFence := false
	if chain {
		e.replacementMu.RLock()
		chainFence = true
	}
	defer func() {
		if chainFence {
			e.replacementMu.RUnlock()
		}
	}()
	t.Mu.Lock()
	var old, candidate *lock.Ticket
	var next ids.TxRef
	installedNext := false
	defer func() {
		if !installedNext {
			e.endSerializable(next, false)
		}
		finishNotificationBoundary(t)
		t.Mu.Unlock()
		e.endLockOwner(candidate)
		e.endLockOwner(old)
	}()
	if err := e.transactionAuthority(op); err != nil {
		return EndResult{}, err
	}
	if err := t.Validate(ref); err != nil {
		return EndResult{}, err
	}
	if !sqlEnd {
		// Only exact lease/ref authority reaches this real post-admission
		// context check. A failed native Commit disposes the accepted old
		// incarnation; Rollback still rejects cancellation without clearing it.
		if err := op.Context().Err(); err != nil {
			if commit {
				old = t.Ticket
				t.Ticket = nil
				t.Session.Settings.FinishTransaction(false)
				e.endSerializable(t.Ref, false)
				clearTransaction(t)
				t.Session.RetireTransactionTimeout()
			}
			return EndResult{}, err
		}
	}
	isolation, readOnly, deferrable := t.Isolation, t.ReadOnly, t.Deferrable
	if chain && (!commit || t.State == TxAborted) && len(t.Savepoints) != 0 {
		// Abort pops every retained child before PostgreSQL selects CHAIN
		// characteristics. Healthy commit instead carries the current child
		// mode; do not apply abort unwinding to that distinct disposition.
		readOnly = t.Savepoints[0].ReadOnly
	}
	if chain {
		if t.Next == ids.TxID(^uint64(0)) {
			return EndResult{}, errors.New("stuntgres: transaction identity exhausted")
		}
		e.mu.Lock()
		op.Session.Mu.Lock()
		err := publicationOperationLocked(e, op)
		var diagnostic uint32
		if err == nil {
			diagnostic, err = e.visibility.NextDiagnosticTxLocked()
		}
		if err == nil {
			t.Next++
			next = e.catalog.Domain.Tx(t.Session.SessionToken, t.Next, e.catalog.state.Epoch(), t, diagnostic)
			e.enrollSerializableLocked(next, isolation)
		}
		op.Session.Mu.Unlock()
		e.mu.Unlock()
		if err != nil {
			return EndResult{}, err
		}
		t.Mu.Unlock()
		var errRegister error
		candidate, errRegister = e.registerTransactionLocks(next)
		t.Mu.Lock()
		if errRegister != nil {
			return EndResult{}, errRegister
		}
	}
	// Invalid refs never reach disposal. An admitted failed commit, unlike a
	// failed rollback preflight, ends the old incarnation with its original error.
	var err error
	committed := false
	if commit && t.State != TxAborted {
		if len(ownerExtensionOrZero(t).exclusionModes.events) != 0 || payloadOf(op.Session).cursors.hasCurrentHold(ref) {
			if scope == nil {
				scope = newStatementScope(op, t, statementOperationStart(op))
			}
			scope.attachTransactionLocked()
		}
		t.Mu.Unlock()
		if chainFence {
			e.replacementMu.RUnlock()
			chainFence = false
		}
		err = e.holdPrecommit(op, t, nextStart, scope)
		if err == nil {
			if scope != nil {
				err = e.checkTransactionExclusions(scope)
			}
		}
		if scope != nil && len(ownerExtensionOrZero(t).foreign.participants) != 0 {
			scope.foreignUnlocked()
		}
		if cleanupErr := disposeSQLCursors(op.Context(), payloadOf(t.Session).cursors.endTransaction(ref)); err == nil {
			err = cleanupErr
		}
		hasForeign := len(ownerExtensionOrZero(t).foreign.participants) != 0
		if chain {
			e.replacementMu.RLock()
			chainFence = true
			e.mu.Lock()
			if err == nil && next.Epoch() != e.catalog.state.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.mu.Unlock()
			if hasForeign {
				e.replacementMu.RUnlock()
				chainFence = false
			}
		}
		if err == nil {
			var publicationScope *StatementScope
			if scope != nil && len(scope.foreignTablesOrZero().statements) != 0 {
				publicationScope = scope
			}
			err = e.publishWrites(op, ref, t.Writes, t.Deletes, publicationScope)
			committed = err == nil
		}
		if scope != nil && len(scope.foreignTablesOrZero().statements) != 0 {
			err = scope.finishForeignStatements(err)
		}
		if scope != nil {
			if hasForeign && !committed {
				err = scope.discardForeignHoldPromotions(err)
			} else {
				scope.foreignTablesSlot.clearHoldPromotions()
			}
		}
		// Old publication truth is independent of prospective CHAIN readmission.
		if chain && hasForeign {
			e.replacementMu.RLock()
			chainFence = true
			e.mu.Lock()
			if err == nil && next.Epoch() != e.catalog.state.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.mu.Unlock()
		}
		t.Mu.Lock()
	} else {
		err = e.endPreflight(op)
		// Replacement also fences SQL recovery commits of aborted blocks.
		if err == nil && commit {
			e.mu.Lock()
			if ref.Epoch() != e.catalog.state.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.mu.Unlock()
		}
	}
	if err != nil && !commit {
		return EndResult{}, err
	}
	if !commit || t.State == TxAborted {
		if chainFence {
			e.replacementMu.RUnlock()
			chainFence = false
		}
		t.Mu.Unlock()
		cleanupErr := disposeSQLCursors(op.Context(), payloadOf(t.Session).cursors.endTransaction(ref))
		if chain {
			e.replacementMu.RLock()
			chainFence = true
			e.mu.Lock()
			if cleanupErr == nil && next.Epoch() != e.catalog.state.Epoch() {
				cleanupErr = ids.ErrCatalogReplaced
			}
			e.mu.Unlock()
		}
		t.Mu.Lock()
		if err == nil {
			err = cleanupErr
		}
	}
	old = t.Ticket
	t.Ticket = nil
	t.Session.Settings.FinishTransaction(committed)
	e.endSerializable(t.Ref, committed)
	clearTransaction(t)
	t.Session.RetireTransactionTimeout()
	if err != nil {
		return EndResult{Committed: committed}, err
	}
	// All fallible work/allocation is over. Publication survives late cancel,
	// and old waiters wake only after this fresh empty incarnation is installed.
	if chain {
		t.Ref = next
		t.State = TxActive
		t.Isolation, t.ReadOnly = isolation, readOnly
		t.Deferrable = deferrable
		// CHAIN restores modes directly: they are the new block's unstacked
		// baseline, unlike BEGIN's overrides of session defaults.
		t.EntryIsolation, t.EntryReadOnly = isolation, readOnly
		t.EntryDeferrable = deferrable
		syncCharacteristics(t)
		t.Start = nextStart
		t.Ticket = candidate
		candidate = nil
		installedNext = true
		t.Session.Mu.Lock()
		t.Session.BeginTimeoutLocked(next, true, time.Now())
		t.Session.Mu.Unlock()
	}
	return EndResult{Committed: committed}, nil
}

func (s *StatementScope) executeControl(control *parser.TransactionControl, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Expected != nil {
		if err := checkStatementSignature(*s.Expected, StatementDescription{}); err != nil {
			return Result{}, err
		}
	}
	e := s.engine()
	if !s.op.CommandAdmission {
		s.op.SnapshotNoticeThreshold()
	}
	t := s.owner
	t.Mu.Lock()
	state, ref := t.State, t.Ref
	t.Mu.Unlock()
	var err error
	if control.Kind == parser.KindPrepareTransaction || control.Kind == parser.KindCommitPrepared || control.Kind == parser.KindRollbackPrepared {
		err = s.executePrepared(control, state, ref)
		if err == nil {
			s.controlOutcome = controlExecuted
		}
		return Result{}, err
	}
	if control.Kind == parser.KindStart {
		if state == TxIdle {
			_, err = e.beginAdmitted(s.op, t, s.transactionStart)
			if err == nil {
				t.Mu.Lock()
				err = assignModes(t, control.Modes)
				t.Mu.Unlock()
			}
		} else if state == TxImplicit {
			err = e.endPreflight(s.op)
			if err == nil {
				t.Mu.Lock()
				t.State = TxActive
				err = assignModes(t, control.Modes)
				t.Mu.Unlock()
				t.Session.Mu.Lock()
				t.Session.Timeouts.Explicit = true
				t.Session.Mu.Unlock()
			}
		} else {
			err = e.endPreflight(s.op)
			if err == nil {
				s.op.EmitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25001", Message: "there is already a transaction in progress"})
			}
		}
	} else if state == TxIdle {
		err = e.endPreflight(s.op)
		if err == nil && control.Chain {
			word := "COMMIT"
			if control.Kind == parser.KindRollback {
				word = "ROLLBACK"
			}
			err = sqlError("25P01", word+" AND CHAIN can only be used in transaction blocks")
		} else if err == nil {
			s.op.EmitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "there is no transaction in progress"})
		}
	} else {
		if state == TxImplicit {
			if control.Chain {
				word := "COMMIT"
				if control.Kind == parser.KindRollback {
					word = "ROLLBACK"
				}
				return Result{}, sqlError("25P01", word+" AND CHAIN can only be used in transaction blocks")
			}
			s.op.EmitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "there is no transaction in progress"})
		}
		nextStart := s.statementStart
		if s.message {
			nextStart = time.Now().UTC().Truncate(time.Microsecond)
		}
		_, err = e.endAdmitted(s.op, t, ref, control.Kind == parser.KindCommit, control.Chain, true, nextStart, s)
	}
	if err == nil {
		s.controlOutcome = controlExecuted
	}
	return Result{}, err
}
