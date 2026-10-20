package engine

import (
	"errors"
	"time"
)

func (e *Engine) newStatementScope(op *Operation, t *transactionOwner, start time.Time) *StatementScope {
	s := newStatementScope(op, t, start)
	s.transactionStart, s.message = start, t.retainedMessage
	if t.retainedMessage && t.messageBorn && t.state == TxIdle {
		s.transactionStart = time.Now().UTC().Truncate(time.Microsecond)
	}
	op.Session.startProvisionalTimeout(&s.lease, op, s.transactionStart)
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
	if err := s.op.RefreshStatementBudget(); err != nil {
		return nil, err
	}
	next := s.engine().newStatementScope(s.op, s.owner, s.statementStart)
	next.message = true
	if s.protocolOrZero().simple {
		next.protocol().simple = true
	}
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
		failed := s.engine().newStatementScope(s.op, t, s.statementStart)
		failed.message = true
		_ = failed.Finish(err)
		t.mu.Lock()
		state, ref = t.state, t.ref
		t.mu.Unlock()
	}
	if state == TxImplicit {
		_, endErr := s.engine().endAdmitted(s.op, t, ref, err == nil, false, true, s.statementStart, s)
		if err == nil {
			err = endErr
		}
	}
	t.mu.Lock()
	t.retainedMessage = false
	finishNotificationBoundary(t)
	t.mu.Unlock()
	s.op.Session.mu.Lock()
	s.op.SQLFinished = true
	s.op.Session.mu.Unlock()
	s.messageEnded = true
	s.messageResult = err
	t.admission.Unlock()
	return err
}
func (s *StatementScope) ownsMessageScope() bool {
	if s.op == nil || s.op.Self != s.op || s.op.Session == nil || s.owner != s.op.Session.transactions {
		return false
	}
	s.op.Session.mu.Lock()
	_, registered := s.op.Session.operations[s.op]
	owned := registered && !s.op.Released && s.op.Lease == &s.lease
	s.op.Session.mu.Unlock()
	return owned
}
