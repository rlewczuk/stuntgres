package core

import (
	"errors"
	"fmt"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

type controlOutcome uint8

const (
	controlOrdinary controlOutcome = iota
	controlDescribed
	controlExecuted
)

type transactionRestart uint8

const (
	restartNone transactionRestart = iota
	restartSQLChain
	restartPL
	restartPLChain
)

func sqlRestart(chain bool) transactionRestart {
	if chain {
		return restartSQLChain
	}
	return restartNone
}

// Entry admission checks the caller context only after structural membership.
// Begin, Rollback and statement admission reject cancellation without mutation.
// Native Commit deliberately admits cancellation to dispose its valid old
// incarnation, preserving the established failed-commit contract.
func (e *Engine) transactionAdmission(op *session.Operation) error {
	if err := session.TransactionAuthority(DomainOf(e), op); err != nil {
		return err
	}
	return op.Context().Err()
}

// Admission is held by the Go entrypoint or the SQL scope, never recursively.
func (e *Engine) beginAdmitted(op *session.Operation, t *session.TransactionOwner, start time.Time) (ids.TxRef, error) {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	if err := e.transactionAdmission(op); err != nil {
		return ids.TxRef{}, err
	}
	if t.State != session.TxIdle {
		return ids.TxRef{}, errors.New("stuntgres: transaction already active")
	}
	if err := e.beginLocked(op, t, session.TxActive, start); err != nil {
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
func (e *Engine) endPreflight(op *session.Operation) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	op.Session.Mu.Lock()
	defer op.Session.Mu.Unlock()
	return PublicationOperationLocked(e, op)
}

func (e *Engine) endAdmitted(op *session.Operation, t *session.TransactionOwner, ref ids.TxRef, commit bool, restart transactionRestart, sqlEnd bool, nextStart time.Time, scope *StatementScope) (EndResult, error) {
	chain := restart != restartNone
	// Registration can fail. A short replacement fence keeps the prospective
	// ticket's epoch current through installation, without a lifetime fence.
	chainFence := false
	if chain {
		e.ReplacementMu.RLock()
		chainFence = true
	}
	defer func() {
		if chainFence {
			e.ReplacementMu.RUnlock()
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
		e.EndLockOwner(candidate)
		e.EndLockOwner(old)
	}()
	if err := session.TransactionAuthority(DomainOf(e), op); err != nil {
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
	var modes session.TxOptions
	if chain {
		modes = t.ChainModes(commit)
		if t.Next == ids.TxID(^uint64(0)) {
			return EndResult{}, errors.New("stuntgres: transaction identity exhausted")
		}
		e.Mu.Lock()
		op.Session.Mu.Lock()
		err := PublicationOperationLocked(e, op)
		var diagnostic uint32
		if err == nil {
			diagnostic, err = e.Visibility.NextDiagnosticTxLocked()
		}
		if err == nil {
			t.Next++
			next = e.Catalog.Domain.Tx(t.Session.SessionToken, t.Next, e.Catalog.State.Epoch(), t, diagnostic)
			if restart == restartSQLChain || restart == restartPLChain {
				e.enrollSerializableLocked(next, modes.Isolation)
			}
		}
		op.Session.Mu.Unlock()
		e.Mu.Unlock()
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
	if commit && t.State != session.TxAborted {
		if len(ownerExtensionOrZero(t).constraintModes.Events) != 0 || PayloadOf(op.Session).Cursors.hasCurrentHold(ref) {
			if scope == nil {
				scope = newStatementScope(op, t, op.StatementStart())
			}
			scope.attachTransactionLocked()
		}
		t.Mu.Unlock()
		if chainFence {
			e.ReplacementMu.RUnlock()
			chainFence = false
		}
		err = e.holdPrecommit(op, t, nextStart, scope)
		if err == nil {
			if scope != nil {
				err = e.checkTransactionConstraints(scope)
			}
		}
		if scope != nil && ownerHasForeign(t) {
			scope.foreignUnlocked()
		}
		if cleanupErr := disposeSQLCursors(op.Context(), PayloadOf(t.Session).Cursors.endTransaction(ref)); err == nil {
			err = cleanupErr
		}
		hasForeign := ownerHasForeign(t)
		if chain {
			e.ReplacementMu.RLock()
			chainFence = true
			e.Mu.Lock()
			if err == nil && next.Epoch() != e.Catalog.State.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.Mu.Unlock()
			if hasForeign {
				e.ReplacementMu.RUnlock()
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
			e.ReplacementMu.RLock()
			chainFence = true
			e.Mu.Lock()
			if err == nil && next.Epoch() != e.Catalog.State.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.Mu.Unlock()
		}
		t.Mu.Lock()
	} else {
		err = e.endPreflight(op)
		// Replacement also fences SQL recovery commits of aborted blocks.
		if err == nil && commit {
			e.Mu.Lock()
			if ref.Epoch() != e.Catalog.State.Epoch() {
				err = ids.ErrCatalogReplaced
			}
			e.Mu.Unlock()
		}
	}
	if err != nil && !commit {
		return EndResult{}, err
	}
	if !commit || t.State == session.TxAborted {
		if chainFence {
			e.ReplacementMu.RUnlock()
			chainFence = false
		}
		t.Mu.Unlock()
		cleanupErr := disposeSQLCursors(op.Context(), PayloadOf(t.Session).Cursors.endTransaction(ref))
		if chain {
			e.ReplacementMu.RLock()
			chainFence = true
			e.Mu.Lock()
			if cleanupErr == nil && next.Epoch() != e.Catalog.State.Epoch() {
				cleanupErr = ids.ErrCatalogReplaced
			}
			e.Mu.Unlock()
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
		t.InstallChain(next, modes, nextStart, candidate)
		if restart == restartPL {
			settings := t.Session.Settings
			settings.Mu.Lock()
			t.BeginLocked(settings, session.TxImplicit, nextStart)
			settings.Mu.Unlock()
			e.Mu.Lock()
			e.enrollSerializableLocked(next, t.Isolation)
			e.Mu.Unlock()
		} else if restart == restartPLChain {
			t.State = session.TxImplicit
		}
		syncCharacteristics(t)
		candidate = nil
		installedNext = true
		t.Session.Mu.Lock()
		t.Session.BeginTimeoutLocked(next, restart == restartSQLChain, time.Now())
		t.Session.Mu.Unlock()
	}
	return EndResult{Committed: committed}, nil
}

func (s *StatementScope) executeControl(control *parser.TransactionControl, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Expected != nil {
		if err := CheckStatementSignature(*s.Expected, Description{}); err != nil {
			return Result{}, err
		}
	}
	e := s.Engine()
	if !s.Op.CommandAdmission {
		s.Op.SnapshotNoticeThreshold()
	}
	t := s.Owner
	t.Mu.Lock()
	state, ref := t.State, t.Ref
	t.Mu.Unlock()
	step := session.ClassifyControl(control.Kind, control.Chain, state)
	var err error
	if control.Kind == parser.KindPrepareTransaction || control.Kind == parser.KindCommitPrepared || control.Kind == parser.KindRollbackPrepared {
		err = s.executePrepared(control, step, state, ref)
		if err == nil {
			s.controlOutcome = controlExecuted
		}
		return Result{}, err
	}
	switch step.Action {
	case session.ControlBegin:
		_, err = e.beginAdmitted(s.Op, t, s.transactionStart)
		if err == nil {
			t.Mu.Lock()
			err = assignModes(t, control.Modes)
			t.Mu.Unlock()
		}
	case session.ControlPromote:
		err = e.endPreflight(s.Op)
		if err == nil {
			t.Mu.Lock()
			t.State = session.TxActive
			err = assignModes(t, control.Modes)
			t.Mu.Unlock()
			t.Session.Mu.Lock()
			t.Session.Timeouts.Explicit = true
			t.Session.Mu.Unlock()
		}
	case session.ControlPreflight:
		err = e.endPreflight(s.Op)
		if err == nil {
			err = step.Err
		}
		if err == nil {
			s.Op.EmitNotice(session.LevelWarning, *step.Warning)
		}
	case session.ControlRefuse:
		return Result{}, step.Err
	default:
		if step.Warning != nil {
			s.Op.EmitNotice(session.LevelWarning, *step.Warning)
		}
		nextStart := s.statementStart
		if s.message {
			nextStart = time.Now().UTC().Truncate(time.Microsecond)
		}
		_, err = e.endAdmitted(s.Op, t, ref, control.Kind == parser.KindCommit, sqlRestart(control.Chain), true, nextStart, s)
	}
	if err == nil {
		s.controlOutcome = controlExecuted
	}
	return Result{}, err
}
