package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Exercise the actual callback decision with an explicit observed instant, not
// sleeps or a fake scheduler. Long real timers are stopped by bounded cleanup.
func timeoutMechanicsSession(t *testing.T) *session.Session {
	t.Helper()
	s, err := New().StartSession(context.Background(), session.SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.WaitEnd(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}

func timeoutMechanicsSettings(s *session.Session, transaction, idleTransaction, idleSession time.Duration, assignment bool, now time.Time) {
	s.Settings.Mu.Lock()
	defer s.Settings.Mu.Unlock()
	if s.Settings.Typed == nil {
		s.Settings.Typed = make(map[string]settings.Value)
	}
	for name, duration := range map[string]time.Duration{
		"transaction_timeout":                 transaction,
		"idle_in_transaction_session_timeout": idleTransaction,
		"idle_session_timeout":                idleSession,
	} {
		s.Settings.Typed[name] = settings.Value{Number: float64(duration / time.Millisecond)}
	}
	s.Mu.Lock()
	s.PublishTimeoutSettingsLocked(s.Settings, assignment, now)
	s.Mu.Unlock()
}

func timeoutMechanicsRef(s *session.Session, id ids.TxID) ids.TxRef {
	return EngineOf(s).Catalog.Domain.Tx(s.Token(), id, 0, s.Transactions, 0)
}

func TestSessionTimeoutGenerationMechanics(t *testing.T) {
	s := timeoutMechanicsSession(t)
	op, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	now := time.Now()
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now)
	scope := NewScope(ScopeFields{StatementStart: now, TransactionStart: now})
	s.StartProvisionalTimeout(ScopeLeaseRef(scope), scope.Op, ScopeTransactionStart(scope))
	s.Transactions.Mu.Lock()
	if s.Transactions.Next != 0 || s.Transactions.State != session.TxIdle {
		t.Error("provisional budget minted transaction state")
	}
	s.Transactions.Mu.Unlock()
	s.Mu.Lock()
	slot := &s.Timeouts.Transaction
	deadline, generation := slot.Deadline, slot.Generation
	first := timeoutMechanicsRef(s, 1)
	s.BeginTimeoutLocked(first, true, now.Add(time.Minute))
	if slot.Lease != nil || slot.Ref != first || slot.Deadline != deadline || slot.Generation != generation {
		t.Error("transaction birth did not adopt the same provisional budget")
	}
	foreign := s.SessionToken
	foreign = withSessionID(foreign, foreign.ID()+1)
	s.ExpireTimeoutLocked(slot, generation, foreign, "25P04", deadline)
	s.Timeouts.Authority = timeoutMechanicsRef(s, 2)
	s.ExpireTimeoutLocked(slot, generation, s.SessionToken, "25P04", deadline)
	if s.State != session.Live {
		t.Error("foreign identity expired the owner")
	}
	s.RetireTransactionTimeoutLocked()
	second := timeoutMechanicsRef(s, 2)
	s.BeginTimeoutLocked(second, true, now.Add(time.Minute))
	s.ExpireTimeoutLocked(slot, generation, s.SessionToken, "25P04", slot.Deadline)
	s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", slot.Deadline.Add(-time.Nanosecond))
	if s.State != session.Live {
		t.Error("stale or early callback expired the next incarnation")
	}
	s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", slot.Deadline)
	if s.State != session.Ending || s.Reason != session.Terminated {
		t.Error("current expiry did not request canonical physical end")
	}
	s.Mu.Unlock()
	if !errors.Is(op.Context().Err(), context.Canceled) {
		t.Fatal("accepted lease was not canceled")
	}
	var diagnostic *sqlstate.Error
	if err := op.CommandError(op.Context().Err()); !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "25P04", Message: "terminating connection due to transaction timeout"}) {
		t.Fatalf("accepted fatal: %#v %v", diagnostic, err)
	}
	diagnostic.Position = "13"
	if err := s.Check(context.Background()); !errors.As(err, &diagnostic) || diagnostic.Position != "" {
		t.Error("operation mutated persistent fatal location", err)
	}
	s.RequestEnd(session.CleanClose)
	op.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	s.Mu.Lock()
	if s.Reason != session.Terminated || s.State != session.Ended {
		t.Error("late close replaced fatal first reason", s.Reason, s.State)
	}
	s.Mu.Unlock()
}

func TestSessionTimeoutAssignmentMechanics(t *testing.T) {
	s := timeoutMechanicsSession(t)
	now := time.Now()
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now)
	s.Mu.Lock()
	s.BeginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
	original, generation := s.Timeouts.Transaction.Deadline, s.Timeouts.Transaction.Generation
	s.Mu.Unlock()
	for _, limit := range []time.Duration{time.Hour, 2 * time.Hour} {
		timeoutMechanicsSettings(s, limit, 0, 0, true, now.Add(time.Minute))
		s.Mu.Lock()
		if s.Timeouts.Transaction.Deadline != original || s.Timeouts.Transaction.Generation != generation {
			t.Error("positive assignment extended current budget")
		}
		s.Mu.Unlock()
	}
	timeoutMechanicsSettings(s, 0, 0, 0, true, now)
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now.Add(time.Minute))
	s.Mu.Lock()
	if !s.Timeouts.Transaction.Deadline.IsZero() {
		t.Error("savepoint/transaction restoration armed a budget")
	}
	s.Mu.Unlock()
	timeoutMechanicsSettings(s, time.Hour, 0, 0, true, now.Add(2*time.Minute))
	s.Mu.Lock()
	if s.Timeouts.Transaction.Deadline != now.Add(2*time.Minute+time.Hour) {
		t.Error("explicit assignment did not rearm stopped budget")
	}
	s.Mu.Unlock()
	s.FinishScopeTimeout(&session.StatementLease{TimeoutRef: timeoutMechanicsRef(s, 1)}, nil, false, ids.TxRef{}, true)
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now.Add(3*time.Minute))
	s.Mu.Lock()
	if !s.Timeouts.Transaction.Deadline.IsZero() {
		t.Error("ERROR restoration restarted the transaction timer")
	}
	s.RetireTransactionTimeoutLocked()
	s.BeginTimeoutLocked(timeoutMechanicsRef(s, 2), true, now.Add(4*time.Minute))
	if s.Timeouts.Transaction.Deadline != now.Add(4*time.Minute+time.Hour) {
		t.Error("next incarnation did not receive a fresh restored budget")
	}
	s.Mu.Unlock()
	s.FinishScopeTimeout(&session.StatementLease{TimeoutRef: timeoutMechanicsRef(s, 1)}, nil, false, ids.TxRef{}, true)
	s.Mu.Lock()
	if s.Timeouts.Transaction.Deadline != now.Add(4*time.Minute+time.Hour) {
		t.Error("late previous scope stopped the new CHAIN budget")
	}
	s.RetireTransactionTimeoutLocked()
	if !s.Timeouts.Transaction.Deadline.IsZero() || s.Timeouts.Authority != (ids.TxRef{}) {
		t.Error("transaction retirement retained runtime authority")
	}
	s.Mu.Unlock()
}

func TestSessionTimeoutIdleMechanics(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		explicit                                  bool
		transaction, idleTransaction, idleSession time.Duration
		code                                      string
	}{
		{"disabled", false, 0, 0, 0, ""},
		{"session", false, time.Hour, 0, 2 * time.Hour, "57P05"},
		{"transaction", true, 0, time.Hour, 2 * time.Hour, "25P03"},
		{"idle-earlier", true, 2 * time.Hour, time.Hour, 2 * time.Hour, "25P03"},
		{"equal-suppressed", true, time.Hour, time.Hour, 2 * time.Hour, ""},
		{"longer-suppressed", true, time.Hour, 2 * time.Hour, 2 * time.Hour, ""},
		{"transaction-idle-disabled", true, 0, 0, time.Hour, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := timeoutMechanicsSession(t)
			now := time.Now()
			timeoutMechanicsSettings(s, tc.transaction, tc.idleTransaction, tc.idleSession, false, now)
			s.Mu.Lock()
			s.Timeouts.Explicit = tc.explicit
			s.ReadyTimeoutLocked(now)
			if s.Timeouts.Idle.Deadline.IsZero() != (tc.code == "") {
				t.Error("wrong idle category or configured suppression")
			}
			if tc.code == "" && s.Timeouts.Idle.Timer != nil {
				t.Error("disabled/suppressed idle allocated a timer")
			}
			generation, deadline := s.Timeouts.Idle.Generation, s.Timeouts.Idle.Deadline
			s.Mu.Unlock()
			op, err := s.AcquireActivity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			s.Mu.Lock()
			if !s.Timeouts.Idle.Deadline.IsZero() || s.Timeouts.SQLLeases != 1 {
				t.Error("active SQL retained idle budget")
			}
			s.ExpireTimeoutLocked(&s.Timeouts.Idle, generation, s.SessionToken, tc.code, deadline)
			if s.State != session.Live {
				t.Error("old idle callback expired active work")
			}
			s.Mu.Unlock()
			op.Done()
			s.Mu.Lock()
			if s.Timeouts.SQLLeases != 0 || s.Timeouts.Idle.Deadline.IsZero() != (tc.code == "") {
				t.Error("ready boundary did not restore exclusive idle category")
			}
			if tc.code != "" {
				s.ExpireTimeoutLocked(&s.Timeouts.Idle, s.Timeouts.Idle.Generation, s.SessionToken, tc.code, s.Timeouts.Idle.Deadline)
				if s.Timeouts.Fatal == nil || s.Timeouts.Fatal.Code != tc.code {
					t.Error("wrong idle fatal")
				}
			}
			s.Mu.Unlock()
		})
	}
}

func TestSessionTimeoutCancellationMechanics(t *testing.T) {
	s := timeoutMechanicsSession(t)
	caller, cancel := context.WithCancel(context.Background())
	op, err := s.AcquireActivity(caller)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	cancel()
	s.Mu.Lock()
	s.Timeouts.Fatal = session.TimeoutFatal("25P04")
	s.Mu.Unlock()
	if got := op.CommandError(context.Canceled); got != context.Canceled {
		t.Error("caller cancellation became SQL fatal", got)
	}
	s.RequestEnd(session.CleanClose)
	s.Mu.Lock()
	if s.Reason != session.CleanClose {
		t.Error("clean close lost first reason")
	}
	s.Mu.Unlock()
}

func TestSessionTimeoutPendingOrderMechanics(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "pending-statement-error"
		if completed {
			name = "completed-statement-cannot-retire-budget"
		}
		t.Run(name, func(t *testing.T) {
			s := timeoutMechanicsSession(t)
			op, err := s.AcquireActivity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			now := time.Now()
			timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now)
			scope := NewScope(ScopeFields{Op: op, StatementStart: now, TransactionStart: now})
			s.StartProvisionalTimeout(ScopeLeaseRef(scope), scope.Op, ScopeTransactionStart(scope))
			timed, expireStatement := context.WithCancelCause(op.Ctx)
			s.Mu.Lock()
			op.Ctx = timed
			s.BeginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
			s.Mu.Unlock()
			if completed {
				s.FinishScopeTimeout(ScopeLeaseRef(scope), scope.Op, ScopeMessage(scope), scope.TxRef, false)
			}
			expireStatement(session.StatementTimeoutCause)
			s.Mu.Lock()
			slot := &s.Timeouts.Transaction
			s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", slot.Deadline)
			if completed {
				if s.State != session.Ending || s.Timeouts.Fatal == nil || s.Timeouts.Fatal.Code != "25P04" {
					t.Error("late completed statement timer suppressed transaction fatal")
				}
			} else if s.State != session.Live || !slot.Deadline.IsZero() || s.Timeouts.Fatal != nil {
				t.Error("already-pending statement ERROR lost released priority")
			}
			s.Mu.Unlock()
			if !completed {
				var diagnostic *sqlstate.Error
				if err := op.CommandError(op.Context().Err()); !errors.As(err, &diagnostic) || diagnostic.Code != "57014" || diagnostic.Message != "canceling statement due to statement timeout" {
					t.Error("pending statement diagnostic", err)
				}
			}
		})
	}
	t.Run("both-idle-and-transaction-already-pending", func(t *testing.T) {
		s := timeoutMechanicsSession(t)
		now := time.Now()
		timeoutMechanicsSettings(s, time.Hour, 30*time.Minute, 0, false, now)
		s.Mu.Lock()
		s.BeginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
		s.ReadyTimeoutLocked(now.Add(50 * time.Minute))
		slot := &s.Timeouts.Transaction
		s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", now.Add(90*time.Minute))
		if s.Timeouts.Fatal == nil || s.Timeouts.Fatal.Code != "25P03" {
			t.Error("already-pending idle-in-transaction FATAL lost released priority")
		}
		s.Mu.Unlock()
	})
	t.Run("pending-lock-and-statement-before-fatal", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(session.TimeoutFatal("25P04"))
		now := time.Now()
		for _, tc := range []struct {
			statement, lock time.Time
			code            string
		}{
			{now.Add(-2 * time.Second), now.Add(-time.Second), "57014"},
			{now.Add(-time.Second), now.Add(-2 * time.Second), "55P03"},
			{now.Add(-time.Second), now.Add(-time.Second), "55P03"},
			{time.Time{}, time.Time{}, "25P04"},
		} {
			var diagnostic *sqlstate.Error
			err := lock.WaitError(ctx, lock.Policy{Source: context.Background(), StatementDeadline: tc.statement}, tc.lock, now)
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				t.Error("released queued deadline/fatal order", tc.code, err)
			}
		}
	})
}

func TestSessionTimeoutNoBirthFinishMechanics(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "no-birth-description"
		if failed {
			name = "parse-failure"
		}
		t.Run(name, func(t *testing.T) {
			s := timeoutMechanicsSession(t)
			timeoutMechanicsSettings(s, time.Hour, 0, 0, false, time.Now())
			op, _, err := s.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := EngineOf(s).StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			s.Mu.Lock()
			generation, deadline := s.Timeouts.Transaction.Generation, s.Timeouts.Transaction.Deadline
			if s.Timeouts.Transaction.Lease != ScopeLeaseRef(scope) || deadline.IsZero() {
				t.Error("missing preparse scope budget")
			}
			s.Mu.Unlock()
			var expected error
			if failed {
				expected = errors.New("ordinary parser failure")
			}
			if err := scope.Finish(expected); err != expected {
				t.Error("finish changed original failure", err)
			}
			op.Done()
			s.Transactions.Mu.Lock()
			if s.Transactions.Next != 0 || s.Transactions.State != session.TxIdle {
				t.Error("no-birth finish minted transaction state")
			}
			s.Transactions.Mu.Unlock()
			s.Mu.Lock()
			if !s.Timeouts.Transaction.Deadline.IsZero() || s.Timeouts.Transaction.Timer != nil {
				t.Error("no-birth finish retained runtime timer")
			}
			s.ExpireTimeoutLocked(&s.Timeouts.Transaction, generation, s.SessionToken, "25P04", deadline)
			if s.State != session.Live {
				t.Error("retired provisional callback terminated ready owner")
			}
			s.Mu.Unlock()
		})
	}
}

func TestSessionTimeoutCleanupErrorMechanics(t *testing.T) {
	s, err := New().StartSession(context.Background(), session.SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	// This mechanics unit substitutes a failing callback for the SQL-name owner.
	if err := s.Detach(s.Resources[session.PreparedSQL].Handle, session.CleanClose); err != nil {
		t.Fatal(err)
	}
	cleanupError := errors.New("independent cleanup result")
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.WaitEnd(ctx); !errors.Is(err, cleanupError) {
			t.Error("cleanup result", err)
		}
	})
	op, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	calls := 0
	if _, err := op.Attach(session.PreparedSQL, &sessionOwner{fn: func(token ids.SessionToken, reason session.EndReason) error {
		calls++
		if token != s.SessionToken || reason != session.Terminated {
			t.Error("wrong cleanup authority", token, reason)
		}
		return cleanupError
	}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now)
	s.Mu.Lock()
	s.BeginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
	slot := &s.Timeouts.Transaction
	s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", slot.Deadline)
	if s.State != session.Ending || s.Leases != 1 {
		t.Error("canonical end failed to retain accepted lease drain")
	}
	s.Mu.Unlock()
	s.RequestEnd(session.CleanClose)
	op.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.WaitEnd(ctx); !errors.Is(err, cleanupError) || calls != 1 {
		t.Error("cleanup did not run exactly once", calls, err)
	}
	var fatal *sqlstate.Error
	if err := s.Check(context.Background()); !errors.As(err, &fatal) || fatal.Code != "25P04" || fatal.Severity != "FATAL" {
		t.Error("cleanup result erased terminal diagnostic", err)
	}
}

func TestSessionTimeoutResetAllMechanics(t *testing.T) {
	s := timeoutMechanicsSession(t)
	now := time.Now()
	timeoutMechanicsSettings(s, time.Hour, 2*time.Hour, 3*time.Hour, false, now)
	s.Settings.Mu.Lock()
	for name, value := range map[string]string{
		"transaction_timeout":                 "1h",
		"idle_in_transaction_session_timeout": "2h",
		"idle_session_timeout":                "3h",
	} {
		s.Settings.Values[name] = value
	}
	s.Settings.Mu.Unlock()
	op, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ref, err := EngineOf(s).Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s.Mu.Lock()
	generation, deadline := s.Timeouts.Transaction.Generation, s.Timeouts.Transaction.Deadline
	if deadline.IsZero() || s.Timeouts.Transaction.Ref != ref {
		t.Error("canonical BEGIN did not arm transaction budget")
	}
	s.Mu.Unlock()

	// Invoke the existing batch mutation seam, not a SQL parser/response test.
	if _, err := EngineExecuteSetting(EngineOf(s), op, &parser.Reset{All: true}, 0, false); err != nil {
		t.Fatal(err)
	}
	s.Mu.Lock()
	if s.Timeouts.TransactionLimit != 0 || s.Timeouts.IdleTransactionLimit != 0 || s.Timeouts.IdleSessionLimit != 0 ||
		!s.Timeouts.Transaction.Deadline.IsZero() || s.Timeouts.Transaction.Timer != nil || s.Timeouts.Authority != ref {
		t.Error("batch reset did not publish disabled limits while retaining canonical transaction")
	}
	s.ExpireTimeoutLocked(&s.Timeouts.Transaction, generation, s.SessionToken, "25P04", deadline)
	if s.State != session.Live {
		t.Error("retired pre-reset callback terminated owner")
	}
	s.Mu.Unlock()
	s.Settings.Mu.Lock()
	for _, name := range []string{"transaction_timeout", "idle_in_transaction_session_timeout", "idle_session_timeout"} {
		_, text := s.Settings.Values[name]
		_, typed := s.Settings.Typed[name]
		_, journal := s.Settings.Actions[name]
		if text || typed || !journal {
			t.Error("batch reset bypassed canonical journal/cache", name)
		}
	}
	s.Settings.Mu.Unlock()
	if _, err := EngineOf(s).Rollback(op, ref); err != nil {
		t.Fatal(err)
	}
	s.Mu.Lock()
	if s.Timeouts.TransactionLimit != time.Hour || s.Timeouts.IdleTransactionLimit != 2*time.Hour ||
		s.Timeouts.IdleSessionLimit != 3*time.Hour || !s.Timeouts.Transaction.Deadline.IsZero() || s.Timeouts.Authority != (ids.TxRef{}) {
		t.Error("rollback did not restore settings without rearming retired transaction")
	}
	s.Mu.Unlock()
}
