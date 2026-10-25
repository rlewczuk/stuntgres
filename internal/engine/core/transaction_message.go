package core

import (
	"errors"
	"stuntgres/internal/engine/session"
	"time"
)

func (e *Engine) newStatementScope(op *session.Operation, t *session.TransactionOwner, start time.Time) *StatementScope {
	s := newStatementScope(op, t, start)
	s.transactionStart, s.message = start, t.RetainedMessage
	if t.RetainedMessage && t.MessageBorn && t.State == session.TxIdle {
		s.transactionStart = time.Now().UTC().Truncate(time.Microsecond)
	}
	op.Session.StartProvisionalTimeout(&s.lease, op, s.transactionStart)
	return s
}

// The caller holds owner.mu. Every reached retained command, including a
// control first in a new segment, uses the ordinary incarnation and lock ticket.
func (s *StatementScope) admitTransactionLocked(e *Engine) error {
	t := s.Owner
	if t.State == session.TxIdle {
		if err := e.beginLocked(s.Op, t, session.TxImplicit, s.transactionStart); err != nil {
			return err
		}
	}
	if t.Ticket == nil && t.State != session.TxAborted {
		ref := t.Ref
		t.Mu.Unlock()
		ticket, err := e.registerTransactionLocks(ref)
		t.Mu.Lock()
		if err != nil {
			return err
		}
		t.Ticket = ticket
	}
	s.attachTransactionLocked()
	return nil
}

// RetainMessage extends the already-owned admission, not a synthetic BEGIN.
// The scope remains responsible for ending this message even after SQL controls
// end or replace an incarnation within it.
func (s *StatementScope) RetainMessage() error {
	if err := s.Active(); err != nil {
		return err
	}
	if !s.ownsMessageScope() {
		return errors.New("stuntgres: foreign message admission")
	}
	s.message = true
	s.Owner.Mu.Lock()
	s.Owner.RetainedMessage = true
	s.Owner.MessageBorn = s.Owner.State != session.TxIdle
	s.Owner.Mu.Unlock()
	return nil
}
func (s *StatementScope) NextStatement() (*StatementScope, error) {
	if s == nil || s.self != s || !s.message || !s.Finished || s.messageEnded || !s.ownsMessageScope() {
		return nil, errors.New("stuntgres: inactive message admission")
	}
	if err := s.Op.RefreshStatementBudget(); err != nil {
		return nil, err
	}
	next := s.Engine().newStatementScope(s.Op, s.Owner, s.statementStart)
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
	if !s.Finished {
		err = s.Finish(err)
	}
	t := s.Owner
	t.Mu.Lock()
	state, ref := t.State, t.Ref
	t.Mu.Unlock()
	if err != nil && (state == session.TxImplicit || state == session.TxActive) && s.Finished {
		failed := s.Engine().newStatementScope(s.Op, t, s.statementStart)
		failed.message = true
		_ = failed.Finish(err)
		t.Mu.Lock()
		state, ref = t.State, t.Ref
		t.Mu.Unlock()
	}
	if state == session.TxImplicit {
		_, endErr := s.Engine().endAdmitted(s.Op, t, ref, err == nil, false, true, s.statementStart, s)
		if err == nil {
			err = endErr
		}
	}
	t.Mu.Lock()
	t.RetainedMessage = false
	finishNotificationBoundary(t)
	t.Mu.Unlock()
	s.Op.Session.Mu.Lock()
	s.Op.SQLFinished = true
	s.Op.Session.Mu.Unlock()
	s.messageEnded = true
	s.messageResult = err
	t.Admission.Unlock()
	return err
}
func (s *StatementScope) ownsMessageScope() bool {
	if s.Op == nil || s.Op.Self != s.Op || s.Op.Session == nil || s.Owner != s.Op.Session.Transactions {
		return false
	}
	s.Op.Session.Mu.Lock()
	_, registered := s.Op.Session.Operations[s.Op]
	owned := registered && !s.Op.Released && s.Op.Lease == &s.lease
	s.Op.Session.Mu.Unlock()
	return owned
}
