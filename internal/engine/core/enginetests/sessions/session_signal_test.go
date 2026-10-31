package sessions_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestSessionSignalCommandBoundary(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	control, err := s.AcquireActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Done()
	cancel := func() { s.Mu.Lock(); s.CancelQueryLocked(); s.Mu.Unlock() }
	cancel()
	if control.Context().Err() != nil {
		t.Fatal("incidental activity canceled")
	}
	// Hold settings capture after actual registry admission, reproducing the old
	// AcquireActivity-to-CommandAdmission publication window without a fake lease.
	s.Settings.Mu.Lock()
	type admission struct {
		op  *session.Operation
		err error
	}
	admitted := make(chan admission, 1)
	go func() { op, _, err := s.AcquireCommand(context.Background()); admitted <- admission{op, err} }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.Mu.Lock()
		published := false
		for candidate := range s.Operations {
			if candidate != control {
				published = true
				break
			}
		}
		if published {
			s.CancelQueryLocked()
		}
		s.Mu.Unlock()
		if published {
			break
		}
		if time.Now().After(deadline) {
			s.Settings.Mu.Unlock()
			out := <-admitted
			if out.op != nil {
				out.op.Done()
			}
			t.Fatal("command never reached real admission")
		}
		runtime.Gosched()
	}
	s.Settings.Mu.Unlock()
	out := <-admitted
	if out.err != nil {
		t.Fatal(out.err)
	}
	op := out.op
	if context.Cause(op.Context()) != session.UserCancelCause {
		op.Done()
		t.Fatal("admitted command lost request")
	}
	op.Done()
	next, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Done)
	if next.Context().Err() != nil {
		t.Fatal("idle cancellation poisoned next command")
	}
	nextScope, err := e.StartStatement(next)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			nextScope.Finish(errors.New("test cleanup"))
		}
	})
	if next.Lease == nil {
		t.Fatal("statement lease not published")
	}
	err = nextScope.Finish(nil)
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if next.Context().Err() != nil {
		t.Fatal("completed command canceled")
	}
	next.Done()
	// Repeat admission and retirement on actual operation children; no pending bit
	// can move a request across the Done boundary.
	for range 32 {
		current, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{})
		done := make(chan struct{})
		go func() { close(ready); cancel(); close(done) }()
		<-ready
		current.Done()
		<-done
	}
	fresh, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Done()
	if fresh.Context().Err() != nil {
		t.Fatal("retired command request escaped")
	}
}

func TestSessionSignalCause(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, cause := range []error{context.Canceled, errors.New(session.UserCancelCause.Message), &sqlstate.Error{Code: "57014", Message: session.UserCancelCause.Message}, session.UserCancelCause, session.StatementTimeoutCause} {
		ctx, cancel := context.WithCancelCause(context.Background())
		op, _, err := s.AcquireCommand(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cancel(cause)
		defer op.Done()
		for _, input := range []error{context.Canceled, lock.WaitError(op.Context(), lock.Policy{Source: ctx}, time.Time{}, time.Now())} {
			got := op.CommandError(input)
			var d *sqlstate.Error
			if cause == session.UserCancelCause || cause == session.StatementTimeoutCause {
				message := session.UserCancelCause.Message
				if cause == session.StatementTimeoutCause {
					message = "canceling statement due to statement timeout"
				}
				if !errors.As(got, &d) || d.Code != "57014" || d.Message != message {
					t.Fatal(got)
				}
			} else if got != context.Canceled {
				t.Fatal("foreign cause acquired SQL authority", got)
			}
		}
		primary := &sqlstate.Error{Code: "22P02", Message: "primary", Context: "owned COPY context"}
		if got := op.CommandError(errors.Join(primary, context.Canceled)); !errors.Is(got, primary) {
			t.Fatal(got)
		}
		source := lock.WaitError(op.Context(), lock.Policy{Source: ctx}, time.Time{}, time.Now())
		if got := op.CommandError(errors.Join(primary, source)); !errors.Is(got, primary) {
			t.Fatal("lock cleanup displaced primary", got)
		}
		enriched := *session.UserCancelCause
		enriched.Context = "COPY owned, line 1"
		if got := op.CommandError(&enriched); got != &enriched {
			t.Fatal("owned primary replaced", got)
		}
		op.Done()
	}
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	s.Mu.Lock()
	s.CancelQueryLocked()
	s.TerminateLocked()
	s.Mu.Unlock()
	if context.Cause(op.Context()) != session.UserCancelCause {
		t.Fatal("first cause changed")
	}
	got := op.CommandError(context.Canceled)
	var fatal *sqlstate.Error
	if !errors.As(got, &fatal) || fatal.Code != "57P01" || fatal.Severity != "FATAL" {
		t.Fatal(got)
	}
	enriched := *session.UserCancelCause
	enriched.Context = "COPY owned, line 1"
	got = op.CommandError(&enriched)
	if !errors.As(got, &fatal) || fatal.Code != "57P01" || fatal.Context != enriched.Context {
		t.Fatal(got)
	}
}

func TestSessionSignalTimeoutNormalization(t *testing.T) {
	for _, tc := range []struct{ in, out int64 }{{-1, -1}, {0, 0}, {1, 1}, {2147483647, 2147483647}, {2147483648, -2147483648}, {2147483649, -2147483647}, {4294967295, -1}, {4294967296, 0}, {4294967297, 1}, {-4294967296, 0}, {-9223372036854775808, 0}, {9223372036854775807, -1}} {
		if got := session.SignalTimeout(tc.in); got != tc.out {
			t.Fatal(tc.in, got, tc.out)
		}
	}
}
