package engine

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func lockPending(t *testing.T, e *Engine, n int) {
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

func lockEmpty(t *testing.T, e *Engine) {
	t.Helper()
	lockState := e.Locks.Inspect()
	if len(lockState.Owners) != 0 || len(lockState.Resources) != 0 {
		t.Fatalf("residual lock state: owners=%d entries=%d", len(lockState.Owners), len(lockState.Resources))
	}
}

func lockAsync(e *Engine, s *session.Session, ctx context.Context, q string, prepare bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			if prepare {
				_, err = e.Describe(op, mustLockStatement(q))
			} else {
				var r Result
				r, err = e.Execute(op, mustLockStatement(q), 0, nil)
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

func mustLockStatement(q string) parser.Statement {
	statements, err := parser.Parse(q)
	if err != nil {
		panic(err)
	}
	return statements[0]
}

func lockAwait(t *testing.T, done <-chan error, want error) {
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
