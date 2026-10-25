package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// Publish a due budget without delivering its runtime callback. Admission must
// observe expiry before it can stop an idle slot or create a new statement.
// No public SQL behavior or scheduler ordering is simulated here.
func timeoutMechanicsDueAdmission(t *testing.T, s *session.Session, code string) {
	t.Helper()
	now := time.Now()
	transaction, idleTransaction, idleSession := time.Duration(0), time.Duration(0), time.Duration(0)
	switch code {
	case "25P04":
		transaction = time.Hour
	case "25P03":
		idleTransaction = time.Hour
	case "57P05":
		idleSession = time.Hour
	default:
		t.Fatal("unknown timeout category", code)
	}
	timeoutMechanicsSettings(s, transaction, idleTransaction, idleSession, false, now)
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if code != "57P05" {
		s.BeginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
	}
	s.ReadyTimeoutLocked(now)
	slot := &s.Timeouts.Idle
	if code == "25P04" {
		slot = &s.Timeouts.Transaction
	}
	if slot.Timer == nil || slot.Deadline.IsZero() {
		t.Fatal("timeout category did not arm a budget", code)
	}
	slot.Timer.Stop()
	slot.Timer = nil
	slot.Deadline = now.Add(-time.Second)
}

func TestSessionTimeoutAdmissionMechanics(t *testing.T) {
	for _, entry := range []string{"check", "activity", "command"} {
		for _, code := range []string{"25P04", "25P03", "57P05"} {
			t.Run(entry+"/"+code, func(t *testing.T) {
				s := timeoutMechanicsSession(t)
				timeoutMechanicsDueAdmission(t, s, code)
				var err error
				if entry == "check" {
					err = s.Check(context.Background())
				} else {
					var op *session.Operation
					if entry == "command" {
						op, _, err = s.AcquireCommand(context.Background())
					} else {
						op, err = s.AcquireActivity(context.Background())
					}
					if op != nil {
						op.Done()
						t.Fatal("overdue owner admitted an operation")
					}
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != *session.TimeoutFatal(code) {
					t.Fatalf("overdue admission = %#v, %v; want exact %s FATAL", diagnostic, err, code)
				}
				s.Mu.Lock()
				if s.State == session.Live || s.Reason != session.Terminated || s.Leases != 0 || s.Timeouts.SQLLeases != 0 || len(s.Operations) != 0 {
					t.Error("fatal admission retained live owner or published a lease", s.State, s.Reason, s.Leases, s.Timeouts.SQLLeases, len(s.Operations))
				}
				s.Mu.Unlock()
			})
		}
	}
}

func TestSessionTimeoutRetiredAdmissionMechanics(t *testing.T) {
	for _, code := range []string{"25P04", "25P03", "57P05"} {
		t.Run(code, func(t *testing.T) {
			s := timeoutMechanicsSession(t)
			timeoutMechanicsDueAdmission(t, s, code)
			s.Mu.Lock()
			if code == "25P04" {
				s.RetireTransactionTimeoutLocked()
			} else {
				s.Timeouts.Idle.Stop()
			}
			s.Mu.Unlock()
			if err := s.Check(context.Background()); err != nil {
				t.Fatal("retired budget rejected owner check", err)
			}
			op, err := s.AcquireActivity(context.Background())
			if err != nil {
				t.Fatal("retired budget rejected activity", err)
			}
			op.Done()
			s.Mu.Lock()
			if s.State != session.Live || s.Timeouts.Fatal != nil {
				t.Error("retired budget terminated owner")
			}
			s.Mu.Unlock()
		})
	}
}

func TestSessionTimeoutCanceledAdmissionMechanics(t *testing.T) {
	for _, entry := range []string{"check", "activity"} {
		t.Run(entry, func(t *testing.T) {
			s := timeoutMechanicsSession(t)
			timeoutMechanicsDueAdmission(t, s, "25P04")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			if entry == "check" {
				err = s.Check(ctx)
			} else {
				var op *session.Operation
				op, err = s.AcquireActivity(ctx)
				if op != nil {
					op.Done()
					t.Fatal("canceled caller acquired an operation")
				}
			}
			if err != context.Canceled {
				t.Fatal("caller cancellation became session fatal", err)
			}
			s.Mu.Lock()
			if s.State != session.Live || s.Timeouts.Fatal != nil || s.Leases != 0 {
				t.Error("canceled admission changed owner state")
			}
			s.Mu.Unlock()
		})
	}
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
	return engine.EngineOf(s).Catalog.Domain.Tx(s.Token(), id, 0, s.Transactions, 0)
}

// Exercise the actual callback decision with an explicit observed instant, not
// sleeps or a fake scheduler. Long real timers are stopped by bounded cleanup.
func timeoutMechanicsSession(t *testing.T) *session.Session {
	t.Helper()
	s, err := engine.New().StartSession(context.Background(), session.SessionStart{ID: 1})
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
