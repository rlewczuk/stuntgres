package engine

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// These are package mechanics, not SQL text/response tests. Real canonical
// scopes and queued requests run with long timers; explicit observed instants
// exercise callback decisions without sleeps or callback scheduling assumptions.
func timeoutPendingSession(t *testing.T, e *Engine, id ids.SessionID) *Session {
	t.Helper()
	s, err := e.StartSession(context.Background(), SessionStart{ID: id})
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

func timeoutPendingOperation(t *testing.T, s *Session) *Operation {
	t.Helper()
	op, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}

func timeoutPendingScope(t *testing.T, op *Operation) *StatementScope {
	t.Helper()
	scope, err := op.session.Engine().StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func timeoutPendingControl(t *testing.T, s *Session, kind parser.SavepointControlKind) {
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

func timeoutPendingFixture(t *testing.T, child bool) (*Session, *Session, ids.TxRef, *parser.LockTable) {
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
	timeoutMechanicsSettings(s, 3*time.Hour, 0, 0, false, time.Now())
	s.settings.mu.Lock()
	s.settings.values["transaction_timeout"] = "3h"
	s.settings.mu.Unlock()
	begin := timeoutPendingOperation(t, s)
	ref, err := e.Begin(begin, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parent := "parent"
	if _, err := e.applySetting(begin, s.settings, e.settingsDefinitions["application_name"], &parent, false); err != nil {
		t.Fatal(err)
	}
	begin.Done()
	if child {
		timeoutPendingControl(t, s, parser.CreateSavepoint)
	}
	peer := timeoutPendingOperation(t, holder)
	if _, err := e.Begin(peer, TxOptions{}); err != nil {
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

func timeoutPendingRequest(t *testing.T, e *Engine, op *Operation) *relationRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.locks.mu.Lock()
		var found *relationRequest
		for _, entry := range e.locks.entries {
			for _, request := range entry.pending {
				if request.ctx == op.Context() && !request.terminal {
					found = request
				}
			}
		}
		e.locks.mu.Unlock()
		if found != nil {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatal("exact operation did not queue behind holder")
		}
		runtime.Gosched()
	}
}

func timeoutPendingPublish(t *testing.T, request *relationRequest, now time.Time) {
	t.Helper()
	manager := request.ticket.manager
	var notify *relationRequest
	manager.mu.Lock()
	// The real cancellation callback may already own terminal publication
	// after a failing lifecycle assertion. Only the manager's first terminal
	// transition owns notification; never overwrite or renotify its result.
	if request.terminal {
		manager.mu.Unlock()
		return
	}
	err := relationWaitError(request.ctx, request.policy, request.deadline, now)
	terminalRequest(request, err, &notify)
	manager.settle(request.entry, &notify)
	manager.retire(request.ref, request.entry)
	manager.mu.Unlock()
	notifyRequests(notify)
	if err == nil {
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
		want = *timeoutFatal(code)
	}
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Errorf("diagnostic got=%#v err=%v want=%#v", got, err, want)
	}
}

func timeoutPendingRecovery(t *testing.T, s *Session, ref ids.TxRef, child bool) {
	t.Helper()
	if !s.IsLive() {
		t.Error("recoverable ERROR lost physical owner and recovery authority")
		return
	}
	s.transactions.mu.Lock()
	if s.transactions.state != TxAborted || s.transactions.ref != ref {
		t.Error("ERROR did not retain exact aborted transaction")
	}
	s.transactions.mu.Unlock()
	if child {
		timeoutPendingControl(t, s, parser.RollbackSavepoint)
		s.transactions.mu.Lock()
		if s.transactions.state != TxActive || s.transactions.ref != ref || len(s.transactions.savepoints) != 1 {
			t.Error("child rollback did not recover parent incarnation")
		}
		s.transactions.mu.Unlock()
		s.settings.mu.Lock()
		if s.settings.value("application_name") != "parent" {
			t.Error("child ERROR lost retained parent settings")
		}
		s.settings.mu.Unlock()
		s.mu.Lock()
		if s.state != live || !s.timeouts.transaction.deadline.IsZero() || s.timeouts.fatal != nil {
			t.Error("child recovery rearmed budget or accepted fatal")
		}
		s.mu.Unlock()
	}
	op := timeoutPendingOperation(t, s)
	if _, err := s.Engine().Rollback(op, ref); err != nil {
		t.Error("explicit recovery", err)
	}
	op.Done()
	s.mu.Lock()
	if s.state != live || !s.timeouts.transaction.deadline.IsZero() || s.timeouts.fatal != nil {
		t.Error("ERROR recovery rearmed budget or accepted fatal")
	}
	s.mu.Unlock()
}

func timeoutPendingStatement(op *Operation, deadline time.Time) {
	timed, cancel := context.WithDeadlineCause(op.ctx, deadline, statementTimeoutCause)
	op.session.mu.Lock()
	op.statementDeadline, op.ctx, op.timerCancel = deadline, timed, cancel
	op.session.mu.Unlock()
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
				s, holder, ref, lock := timeoutPendingFixture(t, child)
				op := timeoutPendingOperation(t, s)
				scope := timeoutPendingScope(t, op)
				if child {
					value := "child"
					if _, err := s.Engine().applySetting(op, s.settings, s.Engine().settingsDefinitions["application_name"], &value, false); err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
				}
				s.mu.Lock()
				observed := s.timeouts.transaction.deadline.Add(time.Minute)
				statementDeadline := s.timeouts.transaction.deadline.Add(-time.Hour)
				if phase != "statement-due" {
					op.lockTimeout = time.Hour
				}
				s.mu.Unlock()
				var result error
				if phase == "lock-and-statement-due" || phase == "statement-before-lock" {
					if phase == "statement-before-lock" {
						statementDeadline = time.Now().Add(30 * time.Minute)
					}
					timeoutPendingStatement(op, statementDeadline)
				}
				if phase == "statement-due" {
					timeoutPendingStatement(op, statementDeadline)
					if err := scope.gate(lock); err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
					// The real operation context is still live: its callback has not
					// delivered a cause, though the authoritative deadline is due.
					if op.Context().Err() != nil || context.Cause(op.Context()) != nil {
						t.Fatal("statement context callback was not delayed")
					}
					s.mu.Lock()
					slot := &s.timeouts.transaction
					if op.timeoutScope != scope || op.sqlFinished || scope.timeoutRef != ref || slot.ref != ref {
						t.Error("statement callback lacks exact unfinished operation/scope/incarnation authority")
					}
					s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", observed)
					if s.state != live || !slot.deadline.IsZero() || s.timeouts.fatal != nil {
						t.Error("due statement ERROR accepted physical FATAL before callback delivery")
					}
					s.mu.Unlock()
					result = relationWaitError(op.Context(), relationLockPolicy{source: op.sourceContext, statementDeadline: op.statementDeadline}, time.Time{}, observed)
					result = scope.Finish(result)
				} else {
					returned, finished := make(chan error, 1), make(chan error, 1)
					done := make(chan struct{})
					resume := make(chan struct{})
					var once sync.Once
					release := func() { once.Do(func() { close(resume) }) }
					go func() {
						defer close(done)
						_, err := scope.Execute(lock, 0, nil)
						returned <- err
						<-resume
						finished <- scope.Finish(err)
						op.Done()
					}()
					t.Cleanup(func() {
						release()
						op.cancel(nil)
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("pending worker failed bounded join")
						}
					})
					request := timeoutPendingRequest(t, s.Engine(), op)
					if op.pendingTimeoutRequest.Load() != request {
						t.Error("queue did not publish exact operation request")
					}
					if phase == "lock-error-produced" {
						timeoutPendingPublish(t, request, observed)
						result = timeoutPendingReceive(t, returned)
						if op.pendingTimeoutRequest.Load() != nil || !op.pendingTimeoutError.Load() {
							t.Error("terminal SQL ERROR lost sticky projection before Finish")
						}
					}
					s.mu.Lock()
					slot := &s.timeouts.transaction
					if op.timeoutScope != scope || op.sqlFinished || scope.timeoutRef != ref || slot.ref != ref {
						t.Error("lock callback lacks exact unfinished operation/scope/incarnation authority")
					}
					s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", observed)
					if s.state != live || !slot.deadline.IsZero() || s.timeouts.fatal != nil {
						t.Error("pending lock ERROR accepted physical FATAL")
					}
					s.mu.Unlock()
					if phase != "lock-error-produced" {
						if phase == "lock-due-before-grant" {
							peer := timeoutPendingOperation(t, holder)
							if _, err := holder.Engine().Rollback(peer, holder.transactions.ref); err != nil {
								t.Fatal(err)
							}
							peer.Done()
						} else {
							timeoutPendingPublish(t, request, observed)
						}
						result = timeoutPendingReceive(t, returned)
					}
					release()
					result = timeoutPendingReceive(t, finished)
					if op.pendingTimeoutRequest.Load() != nil || op.pendingTimeoutError.Load() {
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
			s, holder, ref, lock := timeoutPendingFixture(t, false)
			op := timeoutPendingOperation(t, s)
			scope := timeoutPendingScope(t, op)
			if phase == "granted-before-finish" || phase == "canceled-before-finish" {
				op.lockTimeout = time.Hour
				returned := make(chan error, 1)
				done := make(chan struct{})
				go func() { defer close(done); _, err := scope.Execute(lock, 0, nil); returned <- err }()
				t.Cleanup(func() {
					op.cancel(nil)
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("granted worker failed bounded join")
						return
					}
					if !scope.finished {
						scope.Finish(context.Canceled)
					}
				})
				timeoutPendingRequest(t, s.Engine(), op)
				if phase == "canceled-before-finish" {
					op.cancel(nil)
				} else {
					peer := timeoutPendingOperation(t, holder)
					if _, err := holder.Engine().Rollback(peer, holder.transactions.ref); err != nil {
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
				if op.pendingTimeoutRequest.Load() != nil || op.pendingTimeoutError.Load() {
					t.Error("grant/non-SQL terminal retained pending timeout projection")
				}
			} else {
				s.mu.Lock()
				statementDeadline := s.timeouts.transaction.deadline.Add(-time.Hour)
				s.mu.Unlock()
				timeoutPendingStatement(op, statementDeadline)
				if err := scope.gate(lock); err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				if phase == "previous-incarnation" {
					control := timeoutPendingOperation(t, s)
					if _, err := s.Engine().Rollback(control, ref); err != nil {
						t.Fatal(err)
					}
					next, err := s.Engine().Begin(control, TxOptions{})
					if err != nil || next == ref {
						t.Fatal("fresh incarnation", next, err)
					}
					control.Done()
				}
			}
			s.mu.Lock()
			slot := &s.timeouts.transaction
			s.expireTimeoutLocked(slot, slot.generation, s.token, "25P04", slot.deadline)
			if s.state != ending || s.timeouts.fatal == nil || s.timeouts.fatal.Code != "25P04" {
				t.Error("stale completed/granted operation suppressed genuine transaction FATAL")
			}
			s.mu.Unlock()
			if phase == "granted-before-finish" || phase == "canceled-before-finish" {
				timeoutPendingDiagnostic(t, scope.Finish(nil), "25P04")
			}
			op.Done()
		})
	}
}
