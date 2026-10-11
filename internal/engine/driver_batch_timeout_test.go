package engine

import (
	"context"
	"errors"
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
	oldDeadline, oldCancel, command, stamp := m.op.statementDeadline, m.op.timerCancel, m.op.commandContext, m.scope.statementStart
	if oldCancel == nil || oldDeadline.IsZero() {
		t.Fatal("missing selected initial statement budget")
	}
	if _, err := m.step(statement(t, "SET statement_timeout='2h'")); err != nil {
		t.Fatal(err)
	}
	oldCancel()
	if !errors.Is(m.op.ctx.Err(), context.Canceled) || command.Err() != nil {
		t.Fatal("statement timer canceled original command authority")
	}
	next, err := m.scope.NextStatement()
	if err != nil {
		t.Fatal("old timer leaked into suffix", err)
	}
	m.scope = next
	if m.op.commandContext != command || m.op.sourceContext != source || m.op.ctx.Value(batchContextKey{}) != "caller" || !m.op.statementDeadline.After(oldDeadline) || m.scope.statementStart != stamp || m.op.ctx.Err() != nil || m.op.lockTimeout != 2*time.Hour {
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
	if m.op.lockTimeout != 3*time.Hour || m.op.commandContext != command || m.op.sqlFinished {
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
	if m.op.timerCancel != nil || !m.op.statementDeadline.IsZero() || m.op.ctx != command {
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
	ref, start, stamp := s.transactions.ref, s.transactions.start, m.scope.statementStart
	s.mu.Lock()
	deadline := s.timeouts.transaction.deadline
	idle := !s.timeouts.idle.deadline.IsZero()
	s.mu.Unlock()
	if deadline.IsZero() || idle || m.op.sqlFinished {
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
	s.mu.Lock()
	later := s.timeouts.transaction.deadline
	idle = !s.timeouts.idle.deadline.IsZero()
	s.mu.Unlock()
	if s.transactions.ref == ref || !s.transactions.start.After(start) || !later.After(deadline) || m.scope.statementStart != stamp || m.scope.transactionStart != s.transactions.start || idle || m.op.sqlFinished {
		t.Fatal("fresh segment reused prior birth/budget or message admission ended")
	}
	if err = m.end(nil); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	retired := s.timeouts.transaction.deadline.IsZero()
	idleSession := s.timeouts.idle.deadline
	s.mu.Unlock()
	if !retired || idleSession.IsZero() || s.TransactionState() != TxIdle {
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
	s.mu.Lock()
	slot := &s.timeouts.transaction
	generation, deadline := slot.generation, slot.deadline
	if deadline.IsZero() {
		s.mu.Unlock()
		t.Fatal("retained segment has no actual timeout authority")
	}
	s.expireTimeoutLocked(slot, generation, s.token, "25P04", deadline)
	s.mu.Unlock()
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
	if s.IsLive() || s.transactions.retainedMessage || s.transactions.ticket != nil || s.TransactionState() != TxIdle {
		t.Fatal("fatal message retained owner resources")
	}
	lockEmpty(t, e)
}
