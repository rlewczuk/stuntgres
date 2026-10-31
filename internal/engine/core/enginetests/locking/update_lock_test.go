package locking_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestUpdateCoreRowWaitRecheck(t *testing.T) {
	for _, finish := range []string{"commit", "rollback", "predicate", "cancel", "reset"} {
		t.Run(finish, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			c := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, c, "CREATE TABLE t(id INT,a INT,b INT)")
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1,10,0),(2,20,0)")
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "UPDATE t SET a=11 WHERE id=1")
			if finish == "predicate" {
				enginetest.SessionSQL(t, a, "UPDATE t SET id=3 WHERE id=1")
				enginetest.SessionSQL(t, a, "UPDATE t SET id=1 WHERE id=2")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := enginetest.LockAsync(e, b, ctx, "UPDATE t SET b=a WHERE id=1", false)
			enginetest.LockPending(t, e, 1)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			lockState := e.Locks.Inspect()
			rowWait := false
			for resource, entry := range lockState.Resources {
				if resource.ObjectRef == ref && resource.Row != 0 && len(entry.Pending) == 1 {
					rowWait = true
				}
			}
			if !rowWait {
				t.Fatal("not an actual logical-row wait")
			}
			enginetest.TransactionRows(t, c, "SELECT a FROM t ORDER BY id", 10, 20)
			if finish != "predicate" {
				enginetest.SessionSQL(t, c, "UPDATE t SET b=20 WHERE id=2")
			}
			// A concurrent matching INSERT is outside B's already frozen candidate set.
			enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1,99,0)")
			switch finish {
			case "cancel":
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
				enginetest.TransactionEnd(t, a, tx, false)
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				enginetest.TransactionEnd(t, a, tx, false)
				enginetest.SessionSQL(t, c, "CREATE TABLE t(id INT,a INT,b INT)")
				enginetest.SessionSQL(t, c, "INSERT INTO t VALUES(1,7,0)")
				enginetest.SessionSQL(t, c, "UPDATE t SET b=a WHERE id=1")
				enginetest.TransactionRows(t, c, "SELECT b FROM t", 7)
				enginetest.LockEmpty(t, e)
				return
			default:
				enginetest.TransactionEnd(t, a, tx, finish != "rollback")
				enginetest.LockAwait(t, done, nil)
			}
			switch finish {
			case "commit":
				enginetest.TransactionRows(t, c, "SELECT b FROM t ORDER BY a", 11, 20, 0)
			case "rollback":
				enginetest.TransactionRows(t, c, "SELECT b FROM t ORDER BY a", 10, 20, 0)
			case "predicate":
				enginetest.TransactionRows(t, c, "SELECT b FROM t ORDER BY a", 0, 0, 0)
			case "cancel":
				enginetest.TransactionRows(t, c, "SELECT b FROM t ORDER BY a", 0, 20, 0)
				enginetest.SessionSQL(t, c, "UPDATE t SET b=a WHERE a=10")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
