package engine

import (
	"context"
	"testing"
)

func TestLockModesSavepoints(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := transactionBegin(t, a)
	rb := transactionBegin(t, b)
	sessionSQL(t, a, "SAVEPOINT s")
	sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := lockAsync(e, b, ctx, "INSERT INTO t VALUES(7)", false)
	joined := false
	defer func() {
		cancel()
		if !joined {
			lockAwait(t, done, context.Canceled)
		}
		transactionEnd(t, b, rb, false)
		transactionEnd(t, a, ra, false)
	}()
	lockPending(t, e, 1)
	sessionSQL(t, a, "ROLLBACK TO s")
	lockAwait(t, done, nil)
	joined = true
	// Transaction identity survives contribution restoration.
	if a.Transactions.Ticket == nil {
		t.Fatal("rollback-to ended outer lock owner")
	}
	sessionSQL(t, a, "SAVEPOINT alive")
	sessionSQL(t, a, "RELEASE alive")
}
