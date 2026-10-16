package engine

import (
	"context"
	"errors"
	"time"
)

func (e *Engine) newStatementScope(op *Operation, t *transactionOwner, start time.Time) *StatementScope {
	s := newStatementScope(op, t, start)
	s.transactionStart, s.message = start, t.retainedMessage
	if t.retainedMessage && t.messageBorn && t.state == TxIdle {
		s.transactionStart = time.Now().UTC().Truncate(time.Microsecond)
	}
	op.session.startProvisionalTimeout(s)
	return s
}

// The caller holds owner.mu. Every reached retained command, including a
// control first in a new segment, uses the ordinary incarnation and lock ticket.
func (s *StatementScope) admitTransactionLocked(e *Engine) error {
	t := s.owner
	if t.state == TxIdle {
		if err := e.beginLocked(s.op, t, TxImplicit, s.transactionStart); err != nil {
			return err
		}
	}
	if t.ticket == nil && t.state != TxAborted {
		ref := t.ref
		t.mu.Unlock()
		ticket, err := e.registerTransactionLocks(ref)
		t.mu.Lock()
		if err != nil {
			return err
		}
		t.ticket = ticket
	}
	s.attachTransactionLocked()
	return nil
}

// RetainMessage extends the already-owned admission, not a synthetic BEGIN.
// The scope remains responsible for ending this message even after SQL controls
// end or replace an incarnation within it.
func (s *StatementScope) RetainMessage() error {
	if err := s.active(); err != nil {
		return err
	}
	if !s.ownsMessageScope() {
		return errors.New("stuntgres: foreign message admission")
	}
	s.message = true
	s.owner.mu.Lock()
	s.owner.retainedMessage = true
	s.owner.messageBorn = s.owner.state != TxIdle
	s.owner.mu.Unlock()
	return nil
}
func (s *StatementScope) NextStatement() (*StatementScope, error) {
	if s == nil || s.self != s || !s.message || !s.finished || s.messageEnded || !s.ownsMessageScope() {
		return nil, errors.New("stuntgres: inactive message admission")
	}
	if err := s.op.refreshStatementBudget(); err != nil {
		return nil, err
	}
	next := s.op.session.Engine().newStatementScope(s.op, s.owner, s.statementStart)
	next.message = true
	return next, nil
}
func (s *StatementScope) FinishMessage(err error) error {
	if s == nil || s.self != s || !s.message {
		return errors.New("stuntgres: foreign message admission")
	}
	if s.messageEnded {
		return s.messageResult
	}
	if !s.ownsMessageScope() {
		return errors.New("stuntgres: stale message admission")
	}
	if !s.finished {
		err = s.Finish(err)
	}
	t := s.owner
	t.mu.Lock()
	state, ref := t.state, t.ref
	t.mu.Unlock()
	if err != nil && (state == TxImplicit || state == TxActive) && s.finished {
		failed := s.op.session.Engine().newStatementScope(s.op, t, s.statementStart)
		failed.message = true
		_ = failed.Finish(err)
		t.mu.Lock()
		state, ref = t.state, t.ref
		t.mu.Unlock()
	}
	if state == TxImplicit {
		_, endErr := s.op.session.Engine().endAdmitted(s.op, t, ref, err == nil, false, true, s.statementStart, s)
		if err == nil {
			err = endErr
		}
	}
	t.mu.Lock()
	t.retainedMessage = false
	t.finishNotificationBoundary()
	t.mu.Unlock()
	s.op.session.mu.Lock()
	s.op.sqlFinished = true
	s.op.session.mu.Unlock()
	s.messageEnded = true
	s.messageResult = err
	t.admission.Unlock()
	return err
}
func (s *StatementScope) ownsMessageScope() bool {
	if s.op == nil || s.op.self != s.op || s.op.session == nil || s.owner != s.op.session.transactions {
		return false
	}
	s.op.session.mu.Lock()
	_, registered := s.op.session.operations[s.op]
	owned := registered && !s.op.done && s.op.timeoutScope == s
	s.op.session.mu.Unlock()
	return owned
}

// Caller holds the transaction mutex where required by existing gates.
func (t *transactionOwner) inBlock() bool {
	return t.state == TxActive || t.state == TxImplicit && t.retainedMessage
}

func (op *Operation) refreshStatementBudget() error {
	if err := op.commandContext.Err(); err != nil {
		return err
	}
	settings := op.session.settings
	settings.mu.Lock()
	timeout := settings.typedValue("statement_timeout").number
	transactionTimeout := settings.typedValue("transaction_timeout").number
	lockTimeout := settings.typedValue("lock_timeout").number
	threshold, _ := messageLevel(settings.value(clientMinMessages.Name))
	settings.mu.Unlock()
	op.session.mu.Lock()
	defer op.session.mu.Unlock()
	if op.timerCancel != nil {
		op.timerCancel()
		op.timerCancel = nil
	}
	op.ctx = op.commandContext
	op.statementDeadline = time.Time{}
	op.lockTimeout = time.Duration(lockTimeout) * time.Millisecond
	op.noticeThreshold = threshold
	op.sqlFinished = false
	if timeout > 0 && (transactionTimeout == 0 || timeout < transactionTimeout) {
		deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
		op.ctx, op.timerCancel = context.WithDeadlineCause(op.commandContext, deadline, statementTimeoutCause)
		op.statementDeadline = deadline
	}
	return nil
}
