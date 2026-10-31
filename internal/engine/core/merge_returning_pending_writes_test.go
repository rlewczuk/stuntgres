package core_test

import (
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestMergeReturningRowRecheck(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)", "CREATE TABLE source(id INT,delta INT)", "INSERT INTO source VALUES(1,7)"} {
		enginetest.SessionSQL(t, peer, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "UPDATE t SET v=40 WHERE id=1")
	done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING source src ON t.id=src.id WHEN MATCHED THEN UPDATE SET v=t.v+src.delta RETURNING merge_action(),src.delta,old.v,new.v")
	enginetest.RowLockPending(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForNoKeyUpdate)
	// The source changes only after observing the worker's actual row queue.
	enginetest.SessionSQL(t, peer, "UPDATE source SET delta=99")
	enginetest.TransactionEnd(t, holder, tx, true)
	select {
	case out := <-done:
		if out.err != nil || out.result.Count != 1 || out.result.Rows == nil {
			t.Fatal(out.result, out.err)
		}
		defer out.result.Rows.Close()
		if got := enginetest.Tuples(out.result); !reflect.DeepEqual(got, [][]driver.Value{{"UPDATE", int64(7), int64(40), int64(47)}}) {
			t.Fatal("source replay/stale predecessor", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("returning row recheck did not retire")
	}
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 47)
	enginetest.TransactionRows(t, peer, "SELECT delta FROM source", 99)
	enginetest.LockEmpty(t, e)
}
