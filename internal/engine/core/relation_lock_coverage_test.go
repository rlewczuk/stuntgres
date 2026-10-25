package core_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func lockUtility(t *testing.T, s *session.Session) {
	t.Helper()
	r := enginetest.SessionSQL(t, s, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	if r.Count != 0 || r.Rows != nil {
		t.Fatalf("AX utility result: %+v", r)
	}
}

// Unlike public start channels, lockPending observes the real manager queue.
// Cancellation and joining are registered after session cleanup, so they run first.
func lockUtilityAsync(t *testing.T, e *core.Engine, s *session.Session, prepare bool) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	joined := false
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			q := mustLockStatement("LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			if prepare {
				_, err = e.Describe(op, q)
			}
			if err == nil {
				r, executeErr := e.Execute(op, q, 0, nil)
				err = executeErr
				if err == nil && (r.Count != 0 || r.Rows != nil) {
					err = fmt.Errorf("AX utility result: %+v", r)
				}
				if r.Rows != nil {
					r.Rows.Close()
				}
			}
			op.Done()
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("utility did not terminate during cleanup")
			}
		}
	})
	return func() {
		t.Helper()
		enginetest.LockAwait(t, done, nil)
		joined = true
		cancel()
	}
}

func TestRelationLockExclusiveConflict(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t/commit=%t", prepare, commit), func(t *testing.T) {
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				b := enginetest.StartSession(t, e, nil)
				c := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
				ra := enginetest.TransactionBegin(t, a)
				rb := enginetest.TransactionBegin(t, b)
				lockUtility(t, a)
				if prepare {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					op, err := b.Acquire(ctx)
					if err != nil {
						cancel()
						t.Fatal(err)
					}
					d, err := e.Describe(op, mustLockStatement("LOCK TABLE t IN ACCESS EXCLUSIVE MODE"))
					op.Done()
					cancel()
					if err != nil || len(d.Results) != 0 || d.Parameters.Count != 0 {
						t.Fatal("metadata-only utility preparation", d, err)
					}
				}
				await := lockUtilityAsync(t, e, b, prepare)
				enginetest.LockPending(t, e, 1)
				enginetest.TransactionEnd(t, a, ra, commit)
				await()
				rc := enginetest.TransactionBegin(t, c)
				awaitSuccessor := lockUtilityAsync(t, e, c, false)
				enginetest.LockPending(t, e, 1)
				enginetest.TransactionEnd(t, b, rb, commit)
				awaitSuccessor()
				enginetest.TransactionEnd(t, c, rc, false)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestRelationLockHolderPhysicalEnd(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
	enginetest.TransactionBegin(t, a)
	lockUtility(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	rb := enginetest.TransactionBegin(t, b)
	await := lockUtilityAsync(t, e, b, false)
	enginetest.LockPending(t, e, 1)
	a.RequestEnd(session.CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	await()
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
	enginetest.TransactionEnd(t, b, rb, false)
	healthy := enginetest.TransactionBegin(t, b)
	lockUtility(t, b)
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
	enginetest.TransactionEnd(t, b, healthy, false)
	enginetest.LockEmpty(t, e)
}
