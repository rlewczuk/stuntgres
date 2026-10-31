package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestUpdateFromStoredSourceMarksOwnFrontier(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		e := engine.New()
		holder := enginetest.StartSession(t, e, nil)
		writer := enginetest.StartSession(t, e, nil)
		peer := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,n INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(1,100)", "CREATE VIEW marked AS SELECT k,n FROM s FOR UPDATE", "CREATE VIEW wrapped AS SELECT k,n FROM marked"} {
			enginetest.SessionSQL(t, peer, q)
		}
		tx := enginetest.TransactionBegin(t, holder)
		enginetest.SessionSQL(t, holder, "UPDATE s SET n=101")
		ctx, cancel := context.WithCancel(context.Background())
		done := enginetest.LockAsync(e, writer, ctx, "UPDATE t SET v=t.v+wrapped.n FROM wrapped WHERE t.id=wrapped.k RETURNING old.v,new.v,wrapped.n", false)
		enginetest.LockPending(t, e, 1)
		sref := enginetest.CatalogRef(t, e, e.CatalogRead(), "s")
		lockState := e.Locks.Inspect()
		sourceWait := false
		for resource, entry := range lockState.Resources {
			if resource.ObjectRef == sref && resource.Row != 0 && len(entry.Pending) == 1 {
				sourceWait = true
			}
		}
		if !sourceWait {
			t.Fatal("stored source mark was not consumed at its real view frontier")
		}
		// Target capture precedes the source-view mark's wait. The late target
		// occurrence may not join after the mark refreshes its physical source.
		enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,30)")
		if cancelled {
			cancel()
			enginetest.LockAwait(t, done, context.Canceled)
		}
		enginetest.TransactionEnd(t, holder, tx, true)
		if !cancelled {
			enginetest.LockAwait(t, done, nil)
		}
		cancel()
		if cancelled {
			enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 10, 30)
		} else {
			enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 30, 111)
		}
		enginetest.TransactionRows(t, peer, "SELECT n FROM s", 101)
		enginetest.LockEmpty(t, e)
	}
}
