package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestLockModesSavepoints(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra := enginetest.TransactionBegin(t, a)
	rb := enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, a, "SAVEPOINT s")
	enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := enginetest.LockAsync(e, b, ctx, "INSERT INTO t VALUES(7)", false)
	joined := false
	defer func() {
		cancel()
		if !joined {
			enginetest.LockAwait(t, done, context.Canceled)
		}
		enginetest.TransactionEnd(t, b, rb, false)
		enginetest.TransactionEnd(t, a, ra, false)
	}()
	enginetest.LockPending(t, e, 1)
	enginetest.SessionSQL(t, a, "ROLLBACK TO s")
	enginetest.LockAwait(t, done, nil)
	joined = true
	// Transaction identity survives contribution restoration.
	if a.Transactions.Ticket == nil {
		t.Fatal("rollback-to ended outer lock owner")
	}
	enginetest.SessionSQL(t, a, "SAVEPOINT alive")
	enginetest.SessionSQL(t, a, "RELEASE alive")
}
