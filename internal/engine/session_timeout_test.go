package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Exercise the actual callback decision with an explicit observed instant, not
// sleeps or a fake scheduler. Long real timers are stopped by bounded cleanup.
func timeoutMechanicsSession(t *testing.T) *Session {
	t.Helper()
	s, err := New().StartSession(context.Background(), SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.WaitEnd(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}

func timeoutMechanicsSettings(s *Session, transaction, idleTransaction, idleSession time.Duration, assignment bool, now time.Time) {
	s.settings.mu.Lock()
	defer s.settings.mu.Unlock()
	if s.settings.typed == nil {
		s.settings.typed = make(map[string]settingValue)
	}
	for name, duration := range map[string]time.Duration{
		"transaction_timeout":                 transaction,
		"idle_in_transaction_session_timeout": idleTransaction,
		"idle_session_timeout":                idleSession,
	} {
		s.settings.typed[name] = settingValue{number: float64(duration / time.Millisecond)}
	}
	s.mu.Lock()
	s.publishTimeoutSettingsLocked(s.settings, assignment, now)
	s.mu.Unlock()
}

func timeoutMechanicsRef(s *Session, id ids.TxID) ids.TxRef {
	return s.Engine().domain.Tx(s.Token(), id, 0, s.transactions, 0)
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
	scope := &StatementScope{statementStart: now, transactionStart: now}
	s.startProvisionalTimeout(scope)
	s.transactions.mu.Lock()
	if s.transactions.next != 0 || s.transactions.state != TxIdle {
		t.Error("provisional budget minted transaction state")
	}
	s.transactions.mu.Unlock()
	s.mu.Lock()
	slot := &s.timeouts.transaction
	deadline, generation := slot.deadline, slot.generation
	first := timeoutMechanicsRef(s, 1)
	s.beginTimeoutLocked(first, true, now.Add(time.Minute))
	if slot.scope != nil || slot.ref != first || slot.deadline != deadline || slot.generation != generation {
		t.Error("transaction birth did not adopt the same provisional budget")
	}
	foreign := s.token
	foreign = withSessionID(foreign, foreign.ID()+1)
	s.expireTimeoutLocked(slot, generation, foreign, "25P04", deadline)
	s.timeouts.authority = timeoutMechanicsRef(s, 2)
	s.expireTimeoutLocked(slot, generation, s.token, "25P04", deadline)
	if s.state != live {
		t.Error("foreign identity expired the owner")
	}
	s.retireTransactionTimeoutLocked()
	second := timeoutMechanicsRef(s, 2)
	s.beginTimeoutLocked(second, true, now.Add(time.Minute))
	s.expireTimeoutLocked(slot, generation, s.token, "25P04", slot.deadline)
	s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", slot.deadline.Add(-time.Nanosecond))
	if s.state != live {
		t.Error("stale or early callback expired the next incarnation")
	}
	s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", slot.deadline)
	if s.state != ending || s.reason != Terminated {
		t.Error("current expiry did not request canonical physical end")
	}
	s.mu.Unlock()
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
	s.RequestEnd(CleanClose)
	op.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if s.reason != Terminated || s.state != ended {
		t.Error("late close replaced fatal first reason", s.reason, s.state)
	}
	s.mu.Unlock()
}

func TestSessionTimeoutAssignmentMechanics(t *testing.T) {
	s := timeoutMechanicsSession(t)
	now := time.Now()
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now)
	s.mu.Lock()
	s.beginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
	original, generation := s.timeouts.transaction.deadline, s.timeouts.transaction.generation
	s.mu.Unlock()
	for _, limit := range []time.Duration{time.Hour, 2 * time.Hour} {
		timeoutMechanicsSettings(s, limit, 0, 0, true, now.Add(time.Minute))
		s.mu.Lock()
		if s.timeouts.transaction.deadline != original || s.timeouts.transaction.generation != generation {
			t.Error("positive assignment extended current budget")
		}
		s.mu.Unlock()
	}
	timeoutMechanicsSettings(s, 0, 0, 0, true, now)
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now.Add(time.Minute))
	s.mu.Lock()
	if !s.timeouts.transaction.deadline.IsZero() {
		t.Error("savepoint/transaction restoration armed a budget")
	}
	s.mu.Unlock()
	timeoutMechanicsSettings(s, time.Hour, 0, 0, true, now.Add(2*time.Minute))
	s.mu.Lock()
	if s.timeouts.transaction.deadline != now.Add(2*time.Minute+time.Hour) {
		t.Error("explicit assignment did not rearm stopped budget")
	}
	s.mu.Unlock()
	s.finishScopeTimeout(&StatementScope{timeoutRef: timeoutMechanicsRef(s, 1)}, true)
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now.Add(3*time.Minute))
	s.mu.Lock()
	if !s.timeouts.transaction.deadline.IsZero() {
		t.Error("ERROR restoration restarted the transaction timer")
	}
	s.retireTransactionTimeoutLocked()
	s.beginTimeoutLocked(timeoutMechanicsRef(s, 2), true, now.Add(4*time.Minute))
	if s.timeouts.transaction.deadline != now.Add(4*time.Minute+time.Hour) {
		t.Error("next incarnation did not receive a fresh restored budget")
	}
	s.mu.Unlock()
	s.finishScopeTimeout(&StatementScope{timeoutRef: timeoutMechanicsRef(s, 1)}, true)
	s.mu.Lock()
	if s.timeouts.transaction.deadline != now.Add(4*time.Minute+time.Hour) {
		t.Error("late previous scope stopped the new CHAIN budget")
	}
	s.retireTransactionTimeoutLocked()
	if !s.timeouts.transaction.deadline.IsZero() || s.timeouts.authority != (ids.TxRef{}) {
		t.Error("transaction retirement retained runtime authority")
	}
	s.mu.Unlock()
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
			s.mu.Lock()
			s.timeouts.explicit = tc.explicit
			s.readyTimeoutLocked(now)
			if s.timeouts.idle.deadline.IsZero() != (tc.code == "") {
				t.Error("wrong idle category or configured suppression")
			}
			if tc.code == "" && s.timeouts.idle.timer != nil {
				t.Error("disabled/suppressed idle allocated a timer")
			}
			generation, deadline := s.timeouts.idle.generation, s.timeouts.idle.deadline
			s.mu.Unlock()
			op, err := s.AcquireActivity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			s.mu.Lock()
			if !s.timeouts.idle.deadline.IsZero() || s.timeouts.sqlLeases != 1 {
				t.Error("active SQL retained idle budget")
			}
			s.expireTimeoutLocked(&s.timeouts.idle, generation, s.token, tc.code, deadline)
			if s.state != live {
				t.Error("old idle callback expired active work")
			}
			s.mu.Unlock()
			op.Done()
			s.mu.Lock()
			if s.timeouts.sqlLeases != 0 || s.timeouts.idle.deadline.IsZero() != (tc.code == "") {
				t.Error("ready boundary did not restore exclusive idle category")
			}
			if tc.code != "" {
				s.expireTimeoutLocked(&s.timeouts.idle, s.timeouts.idle.generation, s.token, tc.code, s.timeouts.idle.deadline)
				if s.timeouts.fatal == nil || s.timeouts.fatal.Code != tc.code {
					t.Error("wrong idle fatal")
				}
			}
			s.mu.Unlock()
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
	s.mu.Lock()
	s.timeouts.fatal = timeoutFatal("25P04")
	s.mu.Unlock()
	if got := op.CommandError(context.Canceled); got != context.Canceled {
		t.Error("caller cancellation became SQL fatal", got)
	}
	s.RequestEnd(CleanClose)
	s.mu.Lock()
	if s.reason != CleanClose {
		t.Error("clean close lost first reason")
	}
	s.mu.Unlock()
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
			scope := &StatementScope{op: op, statementStart: now, transactionStart: now}
			s.startProvisionalTimeout(scope)
			timed, expireStatement := context.WithCancelCause(op.ctx)
			s.mu.Lock()
			op.ctx = timed
			s.beginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
			s.mu.Unlock()
			if completed {
				s.finishScopeTimeout(scope, false)
			}
			expireStatement(statementTimeoutCause)
			s.mu.Lock()
			slot := &s.timeouts.transaction
			s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", slot.deadline)
			if completed {
				if s.state != ending || s.timeouts.fatal == nil || s.timeouts.fatal.Code != "25P04" {
					t.Error("late completed statement timer suppressed transaction fatal")
				}
			} else if s.state != live || !slot.deadline.IsZero() || s.timeouts.fatal != nil {
				t.Error("already-pending statement ERROR lost released priority")
			}
			s.mu.Unlock()
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
		s.mu.Lock()
		s.beginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
		s.readyTimeoutLocked(now.Add(50 * time.Minute))
		slot := &s.timeouts.transaction
		s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", now.Add(90*time.Minute))
		if s.timeouts.fatal == nil || s.timeouts.fatal.Code != "25P03" {
			t.Error("already-pending idle-in-transaction FATAL lost released priority")
		}
		s.mu.Unlock()
	})
	t.Run("pending-lock-and-statement-before-fatal", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(timeoutFatal("25P04"))
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
			err := relationWaitError(ctx, relationLockPolicy{Source: context.Background(), StatementDeadline: tc.statement}, tc.lock, now)
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
			scope, err := s.Engine().StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			generation, deadline := s.timeouts.transaction.generation, s.timeouts.transaction.deadline
			if s.timeouts.transaction.scope != scope || deadline.IsZero() {
				t.Error("missing preparse scope budget")
			}
			s.mu.Unlock()
			var expected error
			if failed {
				expected = errors.New("ordinary parser failure")
			}
			if err := scope.Finish(expected); err != expected {
				t.Error("finish changed original failure", err)
			}
			op.Done()
			s.transactions.mu.Lock()
			if s.transactions.next != 0 || s.transactions.state != TxIdle {
				t.Error("no-birth finish minted transaction state")
			}
			s.transactions.mu.Unlock()
			s.mu.Lock()
			if !s.timeouts.transaction.deadline.IsZero() || s.timeouts.transaction.timer != nil {
				t.Error("no-birth finish retained runtime timer")
			}
			s.expireTimeoutLocked(&s.timeouts.transaction, generation, s.token, "25P04", deadline)
			if s.state != live {
				t.Error("retired provisional callback terminated ready owner")
			}
			s.mu.Unlock()
		})
	}
}

func TestSessionTimeoutCleanupErrorMechanics(t *testing.T) {
	s, err := New().StartSession(context.Background(), SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	// This mechanics unit substitutes a failing callback for the SQL-name owner.
	if err := s.Detach(s.resources[PreparedSQL].handle, CleanClose); err != nil {
		t.Fatal(err)
	}
	cleanupError := errors.New("independent cleanup result")
	t.Cleanup(func() {
		s.RequestEnd(CleanClose)
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
	if _, err := op.Attach(PreparedSQL, &sessionOwner{fn: func(token ids.SessionToken, reason EndReason) error {
		calls++
		if token != s.token || reason != Terminated {
			t.Error("wrong cleanup authority", token, reason)
		}
		return cleanupError
	}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	timeoutMechanicsSettings(s, time.Hour, 0, 0, false, now)
	s.mu.Lock()
	s.beginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
	slot := &s.timeouts.transaction
	s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", slot.deadline)
	if s.state != ending || s.leases != 1 {
		t.Error("canonical end failed to retain accepted lease drain")
	}
	s.mu.Unlock()
	s.RequestEnd(CleanClose)
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
	s.settings.mu.Lock()
	for name, value := range map[string]string{
		"transaction_timeout":                 "1h",
		"idle_in_transaction_session_timeout": "2h",
		"idle_session_timeout":                "3h",
	} {
		s.settings.values[name] = value
	}
	s.settings.mu.Unlock()
	op, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ref, err := s.Engine().Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	generation, deadline := s.timeouts.transaction.generation, s.timeouts.transaction.deadline
	if deadline.IsZero() || s.timeouts.transaction.ref != ref {
		t.Error("canonical BEGIN did not arm transaction budget")
	}
	s.mu.Unlock()

	// Invoke the existing batch mutation seam, not a SQL parser/response test.
	if _, err := s.Engine().executeSetting(op, &parser.Reset{All: true}, 0, false); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if s.timeouts.transactionLimit != 0 || s.timeouts.idleTransactionLimit != 0 || s.timeouts.idleSessionLimit != 0 ||
		!s.timeouts.transaction.deadline.IsZero() || s.timeouts.transaction.timer != nil || s.timeouts.authority != ref {
		t.Error("batch reset did not publish disabled limits while retaining canonical transaction")
	}
	s.expireTimeoutLocked(&s.timeouts.transaction, generation, s.token, "25P04", deadline)
	if s.state != live {
		t.Error("retired pre-reset callback terminated owner")
	}
	s.mu.Unlock()
	s.settings.mu.Lock()
	for _, name := range []string{"transaction_timeout", "idle_in_transaction_session_timeout", "idle_session_timeout"} {
		_, text := s.settings.values[name]
		_, typed := s.settings.typed[name]
		_, journal := s.settings.actions[name]
		if text || typed || !journal {
			t.Error("batch reset bypassed canonical journal/cache", name)
		}
	}
	s.settings.mu.Unlock()
	if _, err := s.Engine().Rollback(op, ref); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if s.timeouts.transactionLimit != time.Hour || s.timeouts.idleTransactionLimit != 2*time.Hour ||
		s.timeouts.idleSessionLimit != 3*time.Hour || !s.timeouts.transaction.deadline.IsZero() || s.timeouts.authority != (ids.TxRef{}) {
		t.Error("rollback did not restore settings without rearming retired transaction")
	}
	s.mu.Unlock()
}
