package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestSessionSignalCallerCancellation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cause    error
		physical bool
	}{
		{"ordinary", context.Canceled, false},
		{"foreign_plain", errors.New(session.UserCancelCause.Message), false},
		{"foreign_typed", &sqlstate.Error{Code: "57014", Message: session.UserCancelCause.Message}, false},
		{"canonical_wire", session.UserCancelCause, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := enginetest.StartSession(t, engine.New(), nil)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			op, _, err := s.AcquireCommand(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			cancel(tc.cause)
			s.Mu.Lock()
			s.TerminateLocked()
			s.Mu.Unlock()
			if context.Cause(op.Context()) != tc.cause {
				t.Fatal("physical termination replaced the source's first cause")
			}
			check := func(got error) {
				t.Helper()
				if !tc.physical {
					if got != ctx.Err() {
						t.Fatal("ordinary source cancellation acquired SQL authority", got)
					}
					return
				}
				want := sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"}
				var diagnostic *sqlstate.Error
				if !errors.As(got, &diagnostic) || *diagnostic != want {
					t.Fatalf("canonical source fatal %#v (%v), want %#v", diagnostic, got, want)
				}
			}
			check(op.CommandError(ctx.Err()))
			check(op.CommandError(lock.WaitError(op.Context(), lock.Policy{Source: ctx}, time.Time{}, time.Now())))
			s.Mu.Lock()
			publication := op.FatalErrorLocked(ctx.Err())
			s.Mu.Unlock()
			check(publication)
		})
	}
}
