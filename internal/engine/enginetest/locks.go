package enginetest

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// LockEmpty requires that the lock manager of e holds no owner and no
// resource.
func LockEmpty(t testing.TB, e *core.Engine) {
	t.Helper()
	lockState := e.Locks.Inspect()
	if len(lockState.Owners) != 0 || len(lockState.Resources) != 0 {
		t.Fatalf("residual lock state: owners=%d entries=%d", len(lockState.Owners), len(lockState.Resources))
	}
}

// LockPending waits up to five seconds until exactly n lock requests of e are
// pending.
func LockPending(t testing.TB, e *core.Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		got := 0
		for _, entry := range lockState.Resources {
			got += len(entry.Pending)
		}
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending %d, want %d", got, n)
		}
		runtime.Gosched()
	}
}

// LockAwait waits up to five seconds for the result of a LockAsync operation
// and requires errors.Is(err, want).
func LockAwait(t testing.TB, done <-chan error, want error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("got %v want %v", err, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lock operation did not terminate")
	}
}

// LockAsync describes (prepare) or executes q in one operation of s on a new
// goroutine and delivers its error on the returned channel.
func LockAsync(e *core.Engine, s *session.Session, ctx context.Context, q string, prepare bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			if prepare {
				_, err = e.Describe(op, LockStatement(q))
			} else {
				var r core.Result
				r, err = e.Execute(op, LockStatement(q), 0, nil)
				if r.Rows != nil {
					r.Rows.Close()
				}
			}
			op.Done()
		}
		done <- err
	}()
	return done
}

// LockStatement parses the first statement of q and panics on a parse error,
// so it can run off the test goroutine.
func LockStatement(q string) parser.Statement {
	statements, err := parser.Parse(q)
	if err != nil {
		panic(err)
	}
	return statements[0]
}

// ExecuteAsync executes q in one operation of s on a new goroutine, under a
// five-second deadline derived from parent, and returns a function that joins
// the worker and returns its result. The worker runs the real command and
// Finish; at test cleanup the owner cancels and joins it before session
// cleanup, even when an assertion ends the test early.
func ExecuteAsync(t testing.TB, e *core.Engine, s *session.Session, parent context.Context, q string) func() (core.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	type outcome struct {
		result core.Result
		err    error
	}
	done := make(chan outcome, 1)
	joined := false
	go func() {
		op, err := s.Acquire(ctx)
		var result core.Result
		if err == nil {
			result, err = e.Execute(op, LockStatement(q), 0, nil)
			op.Done()
		}
		done <- outcome{result, err}
	}()
	await := func() (core.Result, error) {
		t.Helper()
		select {
		case o := <-done:
			joined = true
			return o.result, o.err
		case <-time.After(5 * time.Second):
			t.Fatal("mixed DML worker not joined")
			return core.Result{}, context.DeadlineExceeded
		}
	}
	t.Cleanup(func() {
		cancel()
		if !joined {
			await()
		}
	})
	return await
}

// RowLockPending waits up to five seconds until worker has a live pending
// request (of mode, or any mode when mode is 0) on row of ref while blocker
// holds it.
func RowLockPending(t testing.TB, e *core.Engine, ref ids.ObjectRef, row ids.RowID, worker *session.Session, blocker *lock.Ticket, mode lock.Mode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: row}]
		found := false
		if entry != nil && entry.Holders[blocker].Mode != 0 {
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == worker.SessionToken && (mode == 0 || request.Mode == mode) {
					found = true
					break
				}
			}
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("missing exact live MERGE row request", row, mode)
		}
		runtime.Gosched()
	}
}

// WaitDiagnostic requires err to be exactly the ERROR diagnostic with code,
// message and, when given, position.
func WaitDiagnostic(t testing.TB, err error, code, message string, position ...string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if len(position) != 0 {
		want.Position = position[0]
	}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic = %#v (%v), want %#v", got, err, want)
	}
}
