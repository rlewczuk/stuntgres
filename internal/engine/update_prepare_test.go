package engine

import (
	"context"
	"testing"
)

func TestUpdateCorePrepareNoRowLocks(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "UPDATE t SET v=2")
	done := lockAsync(e, b, context.Background(), "UPDATE t SET v=v", true)
	lockAwait(t, done, nil)
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
	transactionEnd(t, a, tx, false)
	lockEmpty(t, e)
}
