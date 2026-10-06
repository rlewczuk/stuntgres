package engine

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func lockUtility(t *testing.T, s *Session) {
	t.Helper()
	r := sessionSQL(t, s, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	if r.Count != 0 || r.Rows != nil {
		t.Fatalf("AX utility result: %+v", r)
	}
}

// Unlike public start channels, lockPending observes the real manager queue.
// Cancellation and joining are registered after session cleanup, so they run first.
func lockUtilityAsync(t *testing.T, e *Engine, s *Session, prepare bool) func() {
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
		lockAwait(t, done, nil)
		joined = true
		cancel()
	}
}

func TestRelationLockExclusiveConflict(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t/commit=%t", prepare, commit), func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				c := sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(v INT)")
				ra := transactionBegin(t, a)
				rb := transactionBegin(t, b)
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
				lockPending(t, e, 1)
				transactionEnd(t, a, ra, commit)
				await()
				rc := transactionBegin(t, c)
				awaitSuccessor := lockUtilityAsync(t, e, c, false)
				lockPending(t, e, 1)
				transactionEnd(t, b, rb, commit)
				awaitSuccessor()
				transactionEnd(t, c, rc, false)
				lockEmpty(t, e)
			})
		}
	}
}

func TestRelationLockHolderPhysicalEnd(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(7)")
	transactionBegin(t, a)
	lockUtility(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	rb := transactionBegin(t, b)
	await := lockUtilityAsync(t, e, b, false)
	lockPending(t, e, 1)
	a.RequestEnd(CleanClose)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	await()
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
	transactionEnd(t, b, rb, false)
	healthy := transactionBegin(t, b)
	lockUtility(t, b)
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
	transactionEnd(t, b, healthy, false)
	lockEmpty(t, e)
}
