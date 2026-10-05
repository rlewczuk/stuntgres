package engine

import (
	"errors"
	"fmt"
	"time"

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
	if op == nil || op.self != op || op.session == nil || op.session.token.owner != e {
		return errors.New("stuntgres: foreign or missing operation")
	}
	session := op.session
	session.mu.Lock()
	defer session.mu.Unlock()
	if op.done || session.state != live {
		return errors.New("stuntgres: inactive operation")
	}
	if _, ok := session.operations[op]; !ok {
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
func (e *Engine) beginAdmitted(op *Operation, t *transactionOwner) (TxRef, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := e.transactionAdmission(op); err != nil {
		return TxRef{}, err
	}
	if t.state != TxIdle {
		return TxRef{}, errors.New("stuntgres: transaction already active")
	}
	if err := e.beginLocked(op, t, TxActive); err != nil {
		return TxRef{}, err
	}
	ref := t.ref
	t.mu.Unlock()
	ticket, err := e.locks.register(ref)
	t.mu.Lock()
	if err != nil {
		t.clear()
		return TxRef{}, err
	}
	t.ticket = ticket
	return ref, nil
}

// The owner lock precedes Engine.mu -> Session.mu. Rollback deliberately does
// not validate the old epoch, so native replacement always permits recovery.
func (e *Engine) endPreflight(op *Operation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	return publicationOperationLocked(e, op)
}

func (e *Engine) endAdmitted(op *Operation, t *transactionOwner, ref TxRef, commit, chain, sqlEnd bool) (EndResult, error) {
	// Registration can fail. A short replacement fence keeps the prospective
	// ticket's epoch current through installation, without a lifetime fence.
	if chain {
		e.replacementMu.RLock()
		defer e.replacementMu.RUnlock()
	}
	t.mu.Lock()
	var old, candidate *relationTicket
	defer func() {
		t.mu.Unlock()
		e.locks.end(candidate)
		e.locks.end(old)
	}()
	if err := e.transactionAuthority(op); err != nil {
		return EndResult{}, err
	}
	if err := t.validate(ref); err != nil {
		return EndResult{}, err
	}
	if !sqlEnd {
		// Only exact lease/ref authority reaches this real post-admission
		// context check. A failed native Commit disposes the accepted old
		// incarnation; Rollback still rejects cancellation without clearing it.
		if err := op.Context().Err(); err != nil {
			if commit {
				old = t.ticket
				t.ticket = nil
				t.session.settings.finishTransaction(false)
				t.clear()
			}
			return EndResult{}, err
		}
	}
	var next TxRef
	if chain {
		if t.next == TxID(^uint64(0)) {
			return EndResult{}, errors.New("stuntgres: transaction identity exhausted")
		}
		e.mu.Lock()
		op.session.mu.Lock()
		err := publicationOperationLocked(e, op)
		if err == nil {
			t.next++
			next = TxRef{owner: e, session: t.session.token, id: t.next, epoch: e.state.epoch, attachment: t}
		}
		op.session.mu.Unlock()
		e.mu.Unlock()
		if err != nil {
			return EndResult{}, err
		}
		t.mu.Unlock()
		var errRegister error
		candidate, errRegister = e.locks.register(next)
		t.mu.Lock()
		if errRegister != nil {
			return EndResult{}, errRegister
		}
	}
	// Invalid refs never reach disposal. An admitted failed commit, unlike a
	// failed rollback preflight, ends the old incarnation with its original error.
	var err error
	committed := false
	if commit && t.state != TxAborted {
		err = e.publishWrites(op, ref, t.writes)
		committed = err == nil
	} else {
		err = e.endPreflight(op)
		if err == nil && commit && !sqlEnd {
			e.mu.Lock()
			if ref.epoch != e.state.epoch {
				err = ErrCatalogReplaced
			}
			e.mu.Unlock()
		}
	}
	if err != nil && !commit {
		return EndResult{}, err
	}
	old = t.ticket
	t.ticket = nil
	t.session.settings.finishTransaction(committed)
	t.clear()
	if err != nil {
		return EndResult{}, err
	}
	// All fallible work/allocation is over. Publication survives late cancel,
	// and old waiters wake only after this fresh empty incarnation is installed.
	if chain {
		t.ref = next
		t.state = TxActive
		t.start = time.Now().UTC()
		t.ticket = candidate
		candidate = nil
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
	e := s.op.session.Engine()
	if !s.op.commandAdmission {
		s.op.snapshotNoticeThreshold()
	}
	t := s.owner
	t.mu.Lock()
	state, ref := t.state, t.ref
	t.mu.Unlock()
	var err error
	if control.Kind == parser.KindStart {
		if state == TxIdle {
			_, err = e.beginAdmitted(s.op, t)
		} else {
			err = e.endPreflight(s.op)
			if err == nil {
				s.op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25001", Message: "there is already a transaction in progress"})
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
			s.op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "there is no transaction in progress"})
		}
	} else {
		_, err = e.endAdmitted(s.op, t, ref, control.Kind == parser.KindCommit, control.Chain, true)
	}
	if err == nil {
		s.controlOutcome = controlExecuted
	}
	return Result{}, err
}
