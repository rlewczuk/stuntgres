package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestLockModesViewOrder(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	// Catalog ID order is deliberately opposite definition order.
	for _, q := range []string{"CREATE TABLE later(v INT)", "CREATE TABLE first(v INT)", "CREATE VIEW v AS SELECT first.v FROM first JOIN later USING(v)"} {
		enginetest.SessionSQL(t, a, q)
	}
	ra := enginetest.TransactionBegin(t, a)
	rb := enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, a, "LOCK first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := enginetest.LockAsync(e, b, ctx, "LOCK v IN SHARE MODE", false)
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
	first := enginetest.RoleTableRef(t, e, "first")
	later := enginetest.RoleTableRef(t, e, "later")
	lockState := e.Locks.Inspect()
	pending := len(lockState.Resources[lock.Resource{ObjectRef: first}].Pending)
	laterEntry := lockState.Resources[lock.Resource{ObjectRef: later}]
	if pending != 1 || laterEntry != nil {
		t.Fatal("view definition lock order was sorted/preflighted")
	}
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	joined = true
}
