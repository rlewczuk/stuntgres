package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

func TestRelationLockWaitingDeadlineAndEnd(t *testing.T) {
	for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
		for _, prepare := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%t/%t", q, prepare, explicit), func(t *testing.T) {
					e := New()
					a := sessionStart(t, e, nil)
					b := sessionStart(t, e, nil)
					sessionSQL(t, a, "CREATE TABLE t(v INT)")
					ra := transactionBegin(t, a)
					sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
					var rb ids.TxRef
					if explicit {
						rb = transactionBegin(t, b)
					}
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					done := lockAsync(e, b, ctx, q, prepare)
					lockPending(t, e, 1)
					lockAwait(t, done, context.DeadlineExceeded)
					lockPending(t, e, 0)
					if explicit {
						if b.TransactionState() != TxAborted {
							t.Fatal("not aborted")
						}
						transactionEnd(t, b, rb, false)
					} else if b.TransactionState() != TxIdle {
						t.Fatal("implicit did not recover")
					}
					transactionEnd(t, a, ra, false)
					transactionRows(t, b, "SELECT v FROM t")
					lockEmpty(t, e)
				})
			}
		}
	}
	t.Run("physical end active waiter", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		transactionBegin(t, b)
		done := lockAsync(e, b, context.Background(), "SELECT v FROM t", false)
		lockPending(t, e, 1)
		b.RequestEnd(CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		lockAwait(t, done, context.Canceled)
		lockPending(t, e, 0)
		transactionEnd(t, a, ra, false)
		lockEmpty(t, e)
	})
	t.Run("admitted failed commit", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		sessionSQL(t, a, "INSERT INTO t VALUES(1)")
		ctx, cancel := context.WithCancel(context.Background())
		op, err := a.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		_, err = e.Commit(op, ra)
		op.Done()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		transactionRows(t, b, "SELECT v FROM t")
		lockEmpty(t, e)
	})
	t.Run("empty explicit birth and end", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		for _, commit := range []bool{false, true} {
			ref := transactionBegin(t, a)
			transactionEnd(t, a, ref, commit)
			lockEmpty(t, e)
		}
	})
}
