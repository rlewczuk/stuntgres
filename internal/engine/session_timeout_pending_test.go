package engine

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// These are package mechanics, not SQL text/response tests. Real canonical
// scopes and queued requests run with long timers; explicit observed instants
// exercise callback decisions without sleeps or callback scheduling assumptions.
func timeoutPendingSession(t *testing.T, e *Engine, id ids.SessionID) *session.Session {
	t.Helper()
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: id})
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

func timeoutPendingOperation(t *testing.T, s *session.Session) *session.Operation {
	t.Helper()
	op, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}

func timeoutPendingScope(t *testing.T, op *session.Operation) *StatementScope {
	t.Helper()
	scope, err := EngineOf(op.Session).StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func timeoutPendingControl(t *testing.T, s *session.Session, kind parser.SavepointControlKind) {
	t.Helper()
	op := timeoutPendingOperation(t, s)
	scope := timeoutPendingScope(t, op)
	_, err := scope.Execute(&parser.SavepointControl{Kind: kind, Name: parser.Identifier{Text: "pending_parent"}}, 0, nil)
	err = scope.Finish(err)
	op.Done()
	if err != nil {
		t.Fatal("savepoint transition", err)
	}
}

func timeoutPendingFixture(t *testing.T, child bool) (*session.Session, *session.Session, ids.TxRef, *parser.LockTable) {
	t.Helper()
	return timeoutPendingFixtureWithBudget(t, child, 3*time.Hour)
}

func timeoutPendingFixtureWithBudget(t *testing.T, child bool, transactionBudget time.Duration) (*session.Session, *session.Session, ids.TxRef, *parser.LockTable) {
	t.Helper()
	e := New()
	s, holder := timeoutPendingSession(t, e, 1), timeoutPendingSession(t, e, 2)
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "pending_timeout_data"}}}
	setup := timeoutPendingOperation(t, s)
	_, err := e.Execute(setup, &parser.CreateTable{Name: name, Elements: []parser.TableElement{{Column: &parser.Column{Name: "v", TypeName: parser.TypeName{Name: "int4"}}}}}, 0, nil)
	setup.Done()
	if err != nil {
		t.Fatal("mechanics relation fixture", err)
	}
	timeoutMechanicsSettings(s, transactionBudget, 0, 0, false, time.Now())
	s.Settings.Mu.Lock()
	s.Settings.Values["transaction_timeout"] = transactionBudget.String()
	s.Settings.Mu.Unlock()
	begin := timeoutPendingOperation(t, s)
	ref, err := e.Begin(begin, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parent := "parent"
	if _, err := EngineApplySetting(e, begin, s.Settings, e.Settings.Definitions()["application_name"], &parent, false); err != nil {
		t.Fatal(err)
	}
	begin.Done()
	if child {
		timeoutPendingControl(t, s, parser.CreateSavepoint)
	}
	peer := timeoutPendingOperation(t, holder)
	if _, err := e.Begin(peer, session.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	peerScope := timeoutPendingScope(t, peer)
	_, err = peerScope.Execute(&parser.LockTable{Targets: []parser.LockTarget{{Name: name}}, Mode: parser.LockAccessExclusive}, 0, nil)
	err = peerScope.Finish(err)
	peer.Done()
	if err != nil {
		t.Fatal("unreleased holder", err)
	}
	return s, holder, ref, &parser.LockTable{Targets: []parser.LockTarget{{Name: name}}, Mode: parser.LockAccessShare}
}

func timeoutPendingRequest(t *testing.T, e *Engine, op *session.Operation) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		var found *lock.Request
		for _, entry := range lockState.Resources {
			for _, request := range entry.Pending {
				if request.Policy.TimeoutSlot == &op.LockWait && !request.Terminal {
					found = request.Request
				}
			}
		}
		if found != nil {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatal("exact operation did not queue behind holder")
		}
		runtime.Gosched()
	}
}

func timeoutPendingPublish(t *testing.T, e *Engine, request *lock.Request, now time.Time) {
	t.Helper()
	// The real cancellation callback may already own terminal publication
	// after a failing lifecycle assertion. Only the manager's first terminal
	// transition owns notification; never overwrite or renotify its result.
	if err := e.Locks.ExpireWait(request, now); err == nil && !request.Terminal() {
		t.Fatal("explicit due instant did not produce queued ERROR")
	}
}

func timeoutPendingDiagnostic(t *testing.T, err error, code string) {
	t.Helper()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: "canceling statement due to lock timeout"}
	if code == "57014" {
		want.Message = "canceling statement due to statement timeout"
	}
	if code == "25P04" {
		want = *session.TimeoutFatal(code)
	}
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Errorf("diagnostic got=%#v err=%v want=%#v", got, err, want)
	}
}

func timeoutPendingRecovery(t *testing.T, s *session.Session, ref ids.TxRef, child bool) {
	t.Helper()
	if !s.IsLive() {
		t.Error("recoverable ERROR lost physical owner and recovery authority")
		return
	}
	s.Transactions.Mu.Lock()
	if s.Transactions.State != session.TxAborted || s.Transactions.Ref != ref {
		t.Error("ERROR did not retain exact aborted transaction")
	}
	s.Transactions.Mu.Unlock()
	if child {
		timeoutPendingControl(t, s, parser.RollbackSavepoint)
		s.Transactions.Mu.Lock()
		if s.Transactions.State != session.TxActive || s.Transactions.Ref != ref || len(s.Transactions.Savepoints) != 1 {
			t.Error("child rollback did not recover parent incarnation")
		}
		s.Transactions.Mu.Unlock()
		s.Settings.Mu.Lock()
		if s.Settings.Value("application_name") != "parent" {
			t.Error("child ERROR lost retained parent settings")
		}
		s.Settings.Mu.Unlock()
		s.Mu.Lock()
		if s.State != session.Live || !s.Timeouts.Transaction.Deadline.IsZero() || s.Timeouts.Fatal != nil {
			t.Error("child recovery rearmed budget or accepted fatal")
		}
		s.Mu.Unlock()
	}
	op := timeoutPendingOperation(t, s)
	if _, err := EngineOf(s).Rollback(op, ref); err != nil {
		t.Error("explicit recovery", err)
	}
	op.Done()
	s.Mu.Lock()
	if s.State != session.Live || !s.Timeouts.Transaction.Deadline.IsZero() || s.Timeouts.Fatal != nil {
		t.Error("ERROR recovery rearmed budget or accepted fatal")
	}
	s.Mu.Unlock()
}

func timeoutPendingStatement(op *session.Operation, deadline time.Time) {
	timed, cancel := context.WithDeadlineCause(op.Ctx, deadline, session.StatementTimeoutCause)
	op.Session.Mu.Lock()
	op.StatementDeadline, op.Ctx, op.TimerCancel = deadline, timed, cancel
	op.Session.Mu.Unlock()
}

func timeoutPendingReceive(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("pending worker failed bounded result")
		return nil
	}
}

func TestSessionTimeoutPendingLifecycle(t *testing.T) {
	for _, phase := range []string{"lock-due", "lock-error-produced", "lock-due-before-grant", "lock-and-statement-due", "statement-before-lock", "statement-due"} {
		for _, child := range []bool{false, true} {
			name := phase + "/full"
			if child {
				name = phase + "/child"
			}
			t.Run(name, func(t *testing.T) {
				s, holder, ref, lockStatement := timeoutPendingFixture(t, child)
				op := timeoutPendingOperation(t, s)
				scope := timeoutPendingScope(t, op)
				if child {
					value := "child"
					if _, err := EngineApplySetting(EngineOf(s), op, s.Settings, EngineOf(s).Settings.Definitions()["application_name"], &value, false); err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
				}
				s.Mu.Lock()
				observed := s.Timeouts.Transaction.Deadline.Add(time.Minute)
				statementDeadline := s.Timeouts.Transaction.Deadline.Add(-time.Hour)
				if phase != "statement-due" {
					op.LockTimeout = time.Hour
				}
				s.Mu.Unlock()
				var result error
				if phase == "lock-and-statement-due" || phase == "statement-before-lock" {
					if phase == "statement-before-lock" {
						statementDeadline = time.Now().Add(30 * time.Minute)
					}
					timeoutPendingStatement(op, statementDeadline)
				}
				if phase == "statement-due" {
					timeoutPendingStatement(op, statementDeadline)
					if err := scope.Gate(lockStatement); err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
					// The real operation context is still live: its callback has not
					// delivered a cause, though the authoritative deadline is due.
					if op.Context().Err() != nil || context.Cause(op.Context()) != nil {
						t.Fatal("statement context callback was not delayed")
					}
					s.Mu.Lock()
					slot := &s.Timeouts.Transaction
					if op.Lease != ScopeLeaseRef(scope) || op.SQLFinished || ScopeLease(scope).TimeoutRef != ref || slot.Ref != ref {
						t.Error("statement callback lacks exact unfinished operation/scope/incarnation authority")
					}
					s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", observed)
					if s.State != session.Live || !slot.Deadline.IsZero() || s.Timeouts.Fatal != nil {
						t.Error("due statement ERROR accepted physical FATAL before callback delivery")
					}
					s.Mu.Unlock()
					result = lock.WaitError(op.Context(), lock.Policy{Source: op.SourceContext, StatementDeadline: op.StatementDeadline}, time.Time{}, observed)
					result = scope.Finish(result)
				} else {
					returned, finished := make(chan error, 1), make(chan error, 1)
					done := make(chan struct{})
					resume := make(chan struct{})
					var once sync.Once
					release := func() { once.Do(func() { close(resume) }) }
					go func() {
						defer close(done)
						_, err := scope.Execute(lockStatement, 0, nil)
						returned <- err
						<-resume
						finished <- scope.Finish(err)
						op.Done()
					}()
					t.Cleanup(func() {
						release()
						op.Cancel(nil)
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("pending worker failed bounded join")
						}
					})
					request := timeoutPendingRequest(t, EngineOf(s), op)
					if op.LockWait.Request() != request {
						t.Error("queue did not publish exact operation request")
					}
					if phase == "lock-error-produced" {
						timeoutPendingPublish(t, EngineOf(s), request, observed)
						result = timeoutPendingReceive(t, returned)
						if op.LockWait.Request() != nil || !op.LockWait.Failed() {
							t.Error("terminal SQL ERROR lost sticky projection before Finish")
						}
					}
					s.Mu.Lock()
					slot := &s.Timeouts.Transaction
					if op.Lease != ScopeLeaseRef(scope) || op.SQLFinished || ScopeLease(scope).TimeoutRef != ref || slot.Ref != ref {
						t.Error("lock callback lacks exact unfinished operation/scope/incarnation authority")
					}
					s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", observed)
					if s.State != session.Live || !slot.Deadline.IsZero() || s.Timeouts.Fatal != nil {
						t.Error("pending lock ERROR accepted physical FATAL")
					}
					s.Mu.Unlock()
					if phase != "lock-error-produced" {
						if phase == "lock-due-before-grant" {
							peer := timeoutPendingOperation(t, holder)
							if _, err := EngineOf(holder).Rollback(peer, holder.Transactions.Ref); err != nil {
								t.Fatal(err)
							}
							peer.Done()
						} else {
							timeoutPendingPublish(t, EngineOf(s), request, observed)
						}
						result = timeoutPendingReceive(t, returned)
					}
					release()
					result = timeoutPendingReceive(t, finished)
					if op.LockWait.Request() != nil || op.LockWait.Failed() {
						t.Error("Finish retained stale pending timeout projection")
					}
				}
				code := "55P03"
				if phase == "statement-due" || phase == "statement-before-lock" {
					code = "57014"
				}
				timeoutPendingDiagnostic(t, op.CommandError(result), code)
				op.Done()
				timeoutPendingRecovery(t, s, ref, child)
			})
		}
	}
}

func TestSessionTimeoutPendingCompletedLifecycle(t *testing.T) {
	for _, phase := range []string{"granted-before-finish", "canceled-before-finish", "finished-statement", "previous-incarnation"} {
		t.Run(phase, func(t *testing.T) {
			s, holder, ref, lockStatement := timeoutPendingFixture(t, false)
			op := timeoutPendingOperation(t, s)
			scope := timeoutPendingScope(t, op)
			if phase == "granted-before-finish" || phase == "canceled-before-finish" {
				op.LockTimeout = time.Hour
				returned := make(chan error, 1)
				done := make(chan struct{})
				go func() { defer close(done); _, err := scope.Execute(lockStatement, 0, nil); returned <- err }()
				t.Cleanup(func() {
					op.Cancel(nil)
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("granted worker failed bounded join")
						return
					}
					if !scope.Finished {
						scope.Finish(context.Canceled)
					}
				})
				timeoutPendingRequest(t, EngineOf(s), op)
				if phase == "canceled-before-finish" {
					op.Cancel(nil)
				} else {
					peer := timeoutPendingOperation(t, holder)
					if _, err := EngineOf(holder).Rollback(peer, holder.Transactions.Ref); err != nil {
						t.Fatal(err)
					}
					peer.Done()
				}
				err := timeoutPendingReceive(t, returned)
				if phase == "canceled-before-finish" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("non-SQL terminal", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if op.LockWait.Request() != nil || op.LockWait.Failed() {
					t.Error("grant/non-SQL terminal retained pending timeout projection")
				}
			} else {
				s.Mu.Lock()
				statementDeadline := s.Timeouts.Transaction.Deadline.Add(-time.Hour)
				s.Mu.Unlock()
				timeoutPendingStatement(op, statementDeadline)
				if err := scope.Gate(lockStatement); err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				if phase == "previous-incarnation" {
					control := timeoutPendingOperation(t, s)
					if _, err := EngineOf(s).Rollback(control, ref); err != nil {
						t.Fatal(err)
					}
					next, err := EngineOf(s).Begin(control, session.TxOptions{})
					if err != nil || next == ref {
						t.Fatal("fresh incarnation", next, err)
					}
					control.Done()
				}
			}
			s.Mu.Lock()
			slot := &s.Timeouts.Transaction
			s.ExpireTimeoutLocked(slot, slot.Generation, s.SessionToken, "25P04", slot.Deadline)
			if s.State != session.Ending || s.Timeouts.Fatal == nil || s.Timeouts.Fatal.Code != "25P04" {
				t.Error("stale completed/granted operation suppressed genuine transaction FATAL")
			}
			s.Mu.Unlock()
			if phase == "granted-before-finish" || phase == "canceled-before-finish" {
				timeoutPendingDiagnostic(t, scope.Finish(nil), "25P04")
			}
			op.Done()
		})
	}
}

// Callback ordering is a mechanism contract, not a portable wall-clock SQL
// assumption: if both timers become pending, statement ERROR has priority.
// Keep the transaction-first case in a bubble with actual timers and owners.
func TestSessionTimeoutResidualBeforeStatement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, holder, ref, _ := timeoutPendingFixtureWithBudget(t, false, 500*time.Millisecond)
		e := EngineOf(s)
		start := time.Now()
		time.Sleep(300 * time.Millisecond)
		s.Settings.Mu.Lock()
		s.Settings.Typed["statement_timeout"] = settings.Value{Number: 375}
		s.Settings.Values["statement_timeout"] = "375ms"
		s.Settings.Mu.Unlock()
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal("admitting residual-timeout command", err)
		}
		t.Cleanup(op.Done)
		scope := timeoutPendingScope(t, op)
		if got := op.StatementDeadline.Sub(start); got != 675*time.Millisecond {
			t.Fatalf("statement deadline = %v after BEGIN, want 675ms", got)
		}
		s.Mu.Lock()
		transactionDeadline := s.Timeouts.Transaction.Deadline
		s.Mu.Unlock()
		if got := transactionDeadline.Sub(start); got != 500*time.Millisecond {
			t.Fatalf("transaction deadline = %v after BEGIN, want 500ms", got)
		}
		type outcome struct {
			result Result
			err    error
		}
		finished := make(chan outcome, 1)
		done := make(chan struct{})
		insert := statement(t, "INSERT INTO pending_timeout_data VALUES(8)")
		go func() {
			defer close(done)
			result, err := scope.Execute(insert, 0, nil)
			err = scope.Finish(err)
			err = op.CommandError(err)
			op.Done()
			finished <- outcome{result, err}
		}()
		t.Cleanup(func() {
			op.Cancel(nil)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("residual-timeout worker failed bounded join")
			}
		})
		synctest.Wait()
		select {
		case got := <-finished:
			t.Fatalf("mutation ended before queue observation: %+v", got)
		default:
		}
		request := op.LockWait.Request()
		if request == nil {
			t.Fatal("timed mutation did not publish its exact queued request")
		}
		resource := lock.Resource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "pending_timeout_data")}
		state := e.Locks.Inspect().Resources[resource]
		if state == nil || len(state.Pending) != 1 {
			t.Fatalf("active mutation did not queue behind peer: %#v", state)
		}
		waiter := state.Pending[0]
		if waiter.Request != request || waiter.Ticket != s.Transactions.Ticket || waiter.Mode != lock.RowExclusive ||
			waiter.Terminal || state.Holders[holder.Transactions.Ticket].Mode != lock.AccessExclusive ||
			op.LockWait.Request() != request {
			t.Fatalf("active mutation lost exact queue/owner/mode: %#v", state)
		}
		if ScopeLease(scope).TimeoutRef != ref || scope.TxRef != ref {
			t.Fatal("queued mutation lost original transaction incarnation")
		}
		time.Sleep(199 * time.Millisecond)
		synctest.Wait()
		select {
		case got := <-finished:
			t.Fatalf("mutation ended before transaction deadline: %+v", got)
		default:
		}
		if op.LockWait.Request() != request || request.Terminal() {
			t.Fatal("mutation did not remain queued through BEGIN+499ms")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		var got outcome
		select {
		case got = <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("transaction deadline did not finish blocked mutation")
		}
		want := *session.TimeoutFatal("25P04")
		want.Position = "13" // The INSERT target starts at character 13.
		var diagnostic *sqlstate.Error
		if !errors.As(got.err, &diagnostic) || *diagnostic != want {
			t.Fatalf("residual transaction diagnostic = %#v, want %#v", diagnostic, want)
		}
		if got.result.Rows != nil || got.result.Count != 0 {
			t.Fatal("fatal mutation published a result", got.result)
		}
		if elapsed := time.Since(start); elapsed != 500*time.Millisecond {
			t.Fatalf("transaction fired after %v, want 500ms before statement deadline 675ms", elapsed)
		}
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal("joining physical transaction owner", err)
		}
		if s.IsLive() || !holder.IsLive() || !request.Terminal() || op.LockWait.Request() != nil {
			t.Fatal("fatal callback did not terminate owner and clear its queued request independently of peer")
		}
		peer := timeoutPendingOperation(t, holder)
		if _, err := e.Rollback(peer, holder.Transactions.Ref); err != nil {
			t.Fatal("releasing healthy peer", err)
		}
		peer.Done()
		lockEmpty(t, e)
		if rows := tuples(sessionSQL(t, holder, "SELECT v FROM pending_timeout_data")); len(rows) != 0 {
			t.Fatal("fatal mutation published rows", rows)
		}
		if result := sessionSQL(t, holder, "INSERT INTO pending_timeout_data VALUES(9)"); result.Count != 1 {
			t.Fatal("healthy peer could not publish its own mutation", result)
		}
		rows := tuples(sessionSQL(t, holder, "SELECT v FROM pending_timeout_data"))
		if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != int64(9) {
			t.Fatal("healthy peer readback includes fatal mutation or lost its own write", rows)
		}
		lockEmpty(t, e)
	})
}

// A later idle deadline may legitimately win once both flags are pending.
// Exercise the transaction-first residual interval with actual timers instead
// of assuming a busy wall-clock runner dispatches the first callback promptly.
func TestSessionTimeoutResidualBeforeIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, holder, _, lockStatement := timeoutPendingFixtureWithBudget(t, false, 500*time.Millisecond)
		e := EngineOf(s)
		start := time.Now()
		release := timeoutPendingOperation(t, holder)
		if _, err := e.Rollback(release, holder.Transactions.Ref); err != nil {
			t.Fatal("releasing initial peer holder", err)
		}
		release.Done()
		if result := sessionSQL(t, s, "INSERT INTO pending_timeout_data VALUES(8)"); result.Count != 1 {
			t.Fatal("staging owner mutation", result)
		}
		if rows := tuples(sessionSQL(t, holder, "SELECT v FROM pending_timeout_data")); len(rows) != 0 {
			t.Fatal("private owner mutation was visible before timeout", rows)
		}
		time.Sleep(300 * time.Millisecond)
		assignment := timeoutPendingOperation(t, s)
		idle := "375ms"
		_, err := EngineApplySetting(e, assignment, s.Settings, e.Settings.Definitions()["idle_in_transaction_session_timeout"], &idle, false)
		assignment.Done() // The actual ready boundary arms the idle timer.
		if err != nil {
			t.Fatal("assigning residual idle budget", err)
		}
		s.Mu.Lock()
		transactionDeadline, idleDeadline := s.Timeouts.Transaction.Deadline, s.Timeouts.Idle.Deadline
		s.Mu.Unlock()
		if transactionDeadline.Sub(start) != 500*time.Millisecond || idleDeadline.Sub(start) != 675*time.Millisecond {
			t.Fatalf("residual deadlines: transaction=%v idle=%v, want BEGIN+500ms and BEGIN+675ms", transactionDeadline.Sub(start), idleDeadline.Sub(start))
		}
		beginPeer := timeoutPendingOperation(t, holder)
		peerRef, err := e.Begin(beginPeer, session.TxOptions{})
		beginPeer.Done()
		if err != nil {
			t.Fatal("beginning explicit peer lock owner", err)
		}
		peer := timeoutPendingOperation(t, holder)
		scope := timeoutPendingScope(t, peer)
		s.Transactions.Mu.Lock()
		ownerTicket := s.Transactions.Ticket
		s.Transactions.Mu.Unlock()
		holder.Transactions.Mu.Lock()
		peerTicket := holder.Transactions.Ticket
		holder.Transactions.Mu.Unlock()
		t.Cleanup(peer.Done)
		lockStatement.Mode = parser.LockAccessExclusive
		finished := make(chan error, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, err := scope.Execute(lockStatement, 0, nil)
			err = scope.Finish(err)
			peer.Done()
			finished <- err
		}()
		t.Cleanup(func() {
			peer.Cancel(nil)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("idle residual peer failed bounded join")
			}
		})
		synctest.Wait()
		select {
		case err := <-finished:
			t.Fatalf("peer ended before queue observation: %v", err)
		default:
		}
		resource := lock.Resource{ObjectRef: catalogRef(t, e, e.CatalogRead(), "pending_timeout_data")}
		state := e.Locks.Inspect().Resources[resource]
		if state == nil || len(state.Pending) != 1 {
			t.Fatalf("peer did not queue behind private owner mutation: %#v", state)
		}
		waiter := state.Pending[0]
		request := waiter.Request
		// With no peer SQL timeout, the manager owns the pending request;
		// no operation timeout-slot projection is published.
		if request == nil || waiter.Ticket != peerTicket || waiter.Ticket.Ref() != peerRef ||
			waiter.Mode != lock.AccessExclusive || waiter.Terminal ||
			state.Holders[ownerTicket].Mode != lock.RowExclusive {
			t.Fatalf("idle owner/peer lock authority changed: %#v", state)
		}
		time.Sleep(199 * time.Millisecond)
		synctest.Wait()
		select {
		case err := <-finished:
			t.Fatalf("peer ended before transaction deadline: %v", err)
		default:
		}
		state = e.Locks.Inspect().Resources[resource]
		if !s.IsLive() || request.Terminal() || state == nil || len(state.Pending) != 1 ||
			state.Pending[0].Request != request || state.Pending[0].Ticket != peerTicket ||
			state.Pending[0].Mode != lock.AccessExclusive || state.Pending[0].Terminal ||
			state.Holders[ownerTicket].Mode != lock.RowExclusive {
			t.Fatal("idle owner did not retain its private mutation and lock through BEGIN+499ms")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Fatal("joining idle physical owner", err)
		}
		timeoutPendingDiagnostic(t, s.Check(context.Background()), "25P04")
		if elapsed := time.Since(start); elapsed != 500*time.Millisecond {
			t.Fatalf("idle owner terminated after %v, want 500ms before idle deadline 675ms", elapsed)
		}
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal("owner rollback did not release healthy peer", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("owner rollback did not finish blocked peer")
		}
		if s.IsLive() || !holder.IsLive() || !request.Terminal() || peer.LockWait.Request() != nil {
			t.Fatal("idle fatal did not cleanly retire owner independently of peer")
		}
		endPeer := timeoutPendingOperation(t, holder)
		_, err = e.Rollback(endPeer, peerRef)
		endPeer.Done()
		if err != nil {
			t.Fatal("ending explicit peer after lock grant", err)
		}
		lockEmpty(t, e)
		if rows := tuples(sessionSQL(t, holder, "SELECT v FROM pending_timeout_data")); len(rows) != 0 {
			t.Fatal("idle owner timeout published its private mutation", rows)
		}
		if result := sessionSQL(t, holder, "INSERT INTO pending_timeout_data VALUES(9)"); result.Count != 1 {
			t.Fatal("healthy peer could not publish after idle owner timeout", result)
		}
		rows := tuples(sessionSQL(t, holder, "SELECT v FROM pending_timeout_data"))
		if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != int64(9) {
			t.Fatal("idle timeout lost peer write or retained rolled-back owner write", rows)
		}
		lockEmpty(t, e)
	})
}
