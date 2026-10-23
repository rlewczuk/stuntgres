package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func ownStatementAdmissionTimeout(op *session.Operation, err error) bool {
	return op != nil && op.CommandAdmission &&
		(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) &&
		errors.Is(context.Cause(op.Context()), session.StatementTimeoutCause)
}

// finishStatementAdmissionError consumes held admission, if any. Only a real
// command's own timeout may dispose its originating explicit block. Context is
// not authority: membership, reservation and incarnation are checked under the
// same admission that protects ordinary statement disposal.
func (e *Engine) finishStatementAdmissionError(op *session.Operation, err error, held bool) error {
	if !ownStatementAdmissionTimeout(op, err) {
		if held {
			op.Session.Transactions.Admission.Unlock()
		}
		return err
	}
	t := op.Session.Transactions
	if !held {
		t.Admission.Lock()
	}
	defer t.Admission.Unlock()
	if authorityErr := session.TransactionAuthority(domainOf(e), op); authorityErr != nil {
		return authorityErr
	}
	// A canceled waiter never owns the live COPY operation's disposal.
	if t.CopyLease != nil {
		return err
	}
	t.Mu.Lock()
	origin := op.OriginTransaction
	if origin == (ids.TxRef{}) || origin != t.Ref ||
		(t.State != session.TxActive && t.State != session.TxAborted) ||
		!ownStatementAdmissionTimeout(op, err) {
		t.Mu.Unlock()
		return err
	}
	lease := session.StatementLease{TimeoutRef: origin}
	op.Session.FinishScopeTimeout(&lease, op, false, origin, true)
	endedTicket := e.failExplicitTransactionLocked(op, t, err)
	e.endStatementVisibility(origin, t.State != session.TxIdle && t.Isolation.Stable())
	finishNotificationBoundary(t)
	t.Mu.Unlock()
	// The timeout is already primary, just as in ordinary Finish.
	_ = drainCursorCleanup(t, op.Context(), nil)
	e.endLockOwner(endedTicket)
	return err
}
