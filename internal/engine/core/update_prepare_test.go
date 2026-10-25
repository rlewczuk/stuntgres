package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestUpdateCorePrepareNoRowLocks(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "UPDATE t SET v=2")
	done := enginetest.LockAsync(e, b, context.Background(), "UPDATE t SET v=v", true)
	enginetest.LockAwait(t, done, nil)
	lockState := e.Locks.Inspect()
	for resource, entry := range lockState.Resources {
		if resource.Row != 0 {
			for owner := range entry.Holders {
				if owner.Ref().Session() == b.Token() {
					t.Fatal("Prepare acquired selected row")
				}
			}
		}
	}
	enginetest.TransactionEnd(t, a, tx, false)
	enginetest.LockEmpty(t, e)
}
