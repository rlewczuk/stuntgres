package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

// Publish a due budget without delivering its runtime callback. Admission must
// observe expiry before it can stop an idle slot or create a new statement.
// No public SQL behavior or scheduler ordering is simulated here.
func timeoutMechanicsDueAdmission(t *testing.T, s *Session, code string) {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if code != "57P05" {
		s.beginTimeoutLocked(timeoutMechanicsRef(s, 1), true, now)
	}
	s.readyTimeoutLocked(now)
	slot := &s.timeouts.idle
	if code == "25P04" {
		slot = &s.timeouts.transaction
	}
	if slot.timer == nil || slot.deadline.IsZero() {
		t.Fatal("timeout category did not arm a budget", code)
	}
	slot.timer.Stop()
	slot.timer = nil
	slot.deadline = now.Add(-time.Second)
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
					var op *Operation
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
				if !errors.As(err, &diagnostic) || *diagnostic != *timeoutFatal(code) {
					t.Fatalf("overdue admission = %#v, %v; want exact %s FATAL", diagnostic, err, code)
				}
				s.mu.Lock()
				if s.state == live || s.reason != Terminated || s.leases != 0 || s.timeouts.sqlLeases != 0 || len(s.operations) != 0 {
					t.Error("fatal admission retained live owner or published a lease", s.state, s.reason, s.leases, s.timeouts.sqlLeases, len(s.operations))
				}
				s.mu.Unlock()
			})
		}
	}
}

func TestSessionTimeoutRetiredAdmissionMechanics(t *testing.T) {
	for _, code := range []string{"25P04", "25P03", "57P05"} {
		t.Run(code, func(t *testing.T) {
			s := timeoutMechanicsSession(t)
			timeoutMechanicsDueAdmission(t, s, code)
			s.mu.Lock()
			if code == "25P04" {
				s.retireTransactionTimeoutLocked()
			} else {
				s.timeouts.idle.stop()
			}
			s.mu.Unlock()
			if err := s.Check(context.Background()); err != nil {
				t.Fatal("retired budget rejected owner check", err)
			}
			op, err := s.AcquireActivity(context.Background())
			if err != nil {
				t.Fatal("retired budget rejected activity", err)
			}
			op.Done()
			s.mu.Lock()
			if s.state != live || s.timeouts.fatal != nil {
				t.Error("retired budget terminated owner")
			}
			s.mu.Unlock()
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
				var op *Operation
				op, err = s.AcquireActivity(ctx)
				if op != nil {
					op.Done()
					t.Fatal("canceled caller acquired an operation")
				}
			}
			if err != context.Canceled {
				t.Fatal("caller cancellation became session fatal", err)
			}
			s.mu.Lock()
			if s.state != live || s.timeouts.fatal != nil || s.leases != 0 {
				t.Error("canceled admission changed owner state")
			}
			s.mu.Unlock()
		})
	}
}
