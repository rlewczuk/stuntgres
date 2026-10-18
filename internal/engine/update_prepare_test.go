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
	e.locks.mu.Lock()
	for resource, entry := range e.locks.entries {
		if resource.row != 0 {
			for owner := range entry.holders {
				if owner.ref.Session() == b.Token() {
					e.locks.mu.Unlock()
					t.Fatal("Prepare acquired selected row")
				}
			}
		}
	}
	e.locks.mu.Unlock()
	transactionEnd(t, a, tx, false)
	lockEmpty(t, e)
}
