package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/session"
	"testing"
	"time"
)

type batchContextKey struct{}

func TestDriverBatchCurrentBudgetOriginalContext(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"SET statement_timeout='1h'", "SET transaction_timeout='3h'", "SET lock_timeout='2h'"} {
		sessionSQL(t, s, q)
	}
	source := context.WithValue(context.Background(), batchContextKey{}, "caller")
	m := batchUnitStart(t, s, source)
	defer m.end(context.Canceled)
	oldDeadline, oldCancel, command, stamp := m.op.StatementDeadline, m.op.TimerCancel, m.op.CommandContext, ScopeStatementStart(m.scope)
	if oldCancel == nil || oldDeadline.IsZero() {
		t.Fatal("missing selected initial statement budget")
	}
	if _, err := m.step(statement(t, "SET statement_timeout='2h'")); err != nil {
		t.Fatal(err)
	}
	oldCancel()
	if !errors.Is(m.op.Ctx.Err(), context.Canceled) || command.Err() != nil {
		t.Fatal("statement timer canceled original command authority")
	}
	next, err := m.scope.NextStatement()
	if err != nil {
		t.Fatal("old timer leaked into suffix", err)
	}
	m.scope = next
	if m.op.CommandContext != command || m.op.SourceContext != source || m.op.Ctx.Value(batchContextKey{}) != "caller" || !m.op.StatementDeadline.After(oldDeadline) || ScopeStatementStart(m.scope) != stamp || m.op.Ctx.Err() != nil || m.op.LockTimeout != 2*time.Hour {
		t.Fatal("suffix budget/source/timestamp not refreshed")
	}
	if _, err = m.step(statement(t, "SET lock_timeout='3h'")); err != nil {
		t.Fatal(err)
	}
	next, err = m.scope.NextStatement()
	if err != nil {
		t.Fatal(err)
	}
	m.scope = next
	if m.op.LockTimeout != 3*time.Hour || m.op.CommandContext != command || m.op.SQLFinished {
		t.Fatal("current lock setting or SQL activity stale")
	}
	if _, err = m.step(statement(t, "SET statement_timeout=0")); err != nil {
		t.Fatal(err)
	}
	next, err = m.scope.NextStatement()
	if err != nil {
		t.Fatal(err)
	}
	m.scope = next
	if m.op.TimerCancel != nil || !m.op.StatementDeadline.IsZero() || m.op.Ctx != command {
		t.Fatal("disabled timer retained preceding deadline")
	}
	if err = m.end(nil); err != nil {
		t.Fatal(err)
	}
}
func TestDriverBatchFreshSegmentBudgetAndNoIdle(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"SET transaction_timeout='3h'", "SET idle_in_transaction_session_timeout='4h'", "SET idle_session_timeout='5h'"} {
		sessionSQL(t, s, q)
	}
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	result, err := m.step(statement(t, "SELECT 1"))
	if err != nil {
		t.Fatal(err)
	}
	_ = result.Rows.Close()
	ref, start, stamp := s.Transactions.Ref, s.Transactions.Start, ScopeStatementStart(m.scope)
	s.Mu.Lock()
	deadline := s.Timeouts.Transaction.Deadline
	idle := !s.Timeouts.Idle.Deadline.IsZero()
	s.Mu.Unlock()
	if deadline.IsZero() || idle || m.op.SQLFinished {
		t.Fatal("retained prefix armed idle or lost transaction budget")
	}
	if _, err = m.step(statement(t, "COMMIT")); err != nil {
		t.Fatal(err)
	}
	result, err = m.step(statement(t, "SELECT 2"))
	if err != nil {
		t.Fatal(err)
	}
	_ = result.Rows.Close()
	s.Mu.Lock()
	later := s.Timeouts.Transaction.Deadline
	idle = !s.Timeouts.Idle.Deadline.IsZero()
	s.Mu.Unlock()
	if s.Transactions.Ref == ref || !s.Transactions.Start.After(start) || !later.After(deadline) || ScopeStatementStart(m.scope) != stamp || ScopeTransactionStart(m.scope) != s.Transactions.Start || idle || m.op.SQLFinished {
		t.Fatal("fresh segment reused prior birth/budget or message admission ended")
	}
	if err = m.end(nil); err != nil {
		t.Fatal(err)
	}
	s.Mu.Lock()
	retired := s.Timeouts.Transaction.Deadline.IsZero()
	idleSession := s.Timeouts.Idle.Deadline
	s.Mu.Unlock()
	if !retired || idleSession.IsZero() || s.TransactionState() != session.TxIdle {
		t.Fatal("whole-message completion did not retire/arm correct timers")
	}
}
func TestDriverBatchFatalTransactionBudget(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "SET transaction_timeout='3h'")
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	result, err := m.step(statement(t, "SELECT 1"))
	if err != nil {
		t.Fatal(err)
	}
	defer result.Rows.Close()
	s.Mu.Lock()
	slot := &s.Timeouts.Transaction
	generation, deadline := slot.Generation, slot.Deadline
	if deadline.IsZero() {
		s.Mu.Unlock()
		t.Fatal("retained segment has no actual timeout authority")
	}
	s.ExpireTimeoutLocked(slot, generation, s.SessionToken, "25P04", deadline)
	s.Mu.Unlock()
	_, err = m.scope.NextStatement()
	if err == nil {
		t.Fatal("fatal message admitted suffix")
	}
	err = m.op.CommandError(err)
	timeoutPendingDiagnostic(t, err, "25P04")
	end := m.end(err)
	timeoutPendingDiagnostic(t, end, "25P04")
	wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = s.WaitEnd(wait); err != nil {
		t.Fatal(err)
	}
	if s.IsLive() || s.Transactions.RetainedMessage || s.Transactions.Ticket != nil || s.TransactionState() != session.TxIdle {
		t.Fatal("fatal message retained owner resources")
	}
	lockEmpty(t, e)
}
