package engine

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// The retained description is captured before the competing utility starts.
func ownedLockAsync(e *Engine, s *session.Session, ctx context.Context, ast parser.Statement, description *StatementDescription) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			var scope *StatementScope
			scope, err = e.StartStatement(op)
			if err == nil {
				scope.Expected = description
				_, err = scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
			}
			op.Done()
		}
		done <- err
	}()
	return done
}

func ownedLockQueued(t *testing.T, e *Engine, ref ids.ObjectRef, mode lock.Mode, done <-chan error) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		var request *lock.Request
		if entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == mode {
			request = entry.Pending[0].Request
		}
		if request != nil {
			return request
		}
		select {
		case err := <-done:
			t.Fatalf("OWNED BY operation completed before required %d admission on %v: %v", mode, ref, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("required exact relation admission not observed")
		}
		runtime.Gosched()
	}
}
