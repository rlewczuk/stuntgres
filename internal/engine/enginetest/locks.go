package enginetest

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
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
				_, err = e.Describe(op, lockStatement(q))
			} else {
				var r core.Result
				r, err = e.Execute(op, lockStatement(q), 0, nil)
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

// lockStatement parses the first statement of q and panics on a parse error;
// it runs off the test goroutine.
func lockStatement(q string) parser.Statement {
	statements, err := parser.Parse(q)
	if err != nil {
		panic(err)
	}
	return statements[0]
}
