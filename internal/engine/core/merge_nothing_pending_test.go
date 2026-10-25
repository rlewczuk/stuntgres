package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func mergeNothingPendingOutput(t *testing.T, done <-chan deleteOutcome, count int64, rows [][]driver.Value) {
	t.Helper()
	select {
	case out := <-done:
		if out.err != nil || out.result.Count != count || out.result.Rows == nil {
			t.Fatal(out.result, out.err)
		}
		defer out.result.Rows.Close()
		got := enginetest.Tuples(out.result)
		if len(got) != len(rows) || len(rows) != 0 && !reflect.DeepEqual(got, rows) {
			t.Fatal("omission output", got, rows)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MERGE omission worker did not retire")
	}
}
func TestMergeNothingRowAdmission(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "UPDATE t SET v=40")
	done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DO NOTHING RETURNING merge_action(),t.v")
	mergeNothingPendingOutput(t, done, 0, nil)
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: row}]
	retained := entry != nil && entry.Holders[holder.Transactions.Ticket].Mode != 0
	workerAdmission := false
	if entry != nil {
		for ticket := range entry.Holders {
			workerAdmission = workerAdmission || ticket.Ref().Session() == worker.SessionToken
		}
		for _, request := range entry.Pending {
			workerAdmission = workerAdmission || !request.Terminal && request.Ticket.Ref().Session() == worker.SessionToken
		}
	}
	if !retained || workerAdmission {
		t.Fatal("no-op acquired mutation grant or retired holder", retained, workerAdmission)
	}
	enginetest.TransactionRows(t, holder, "SELECT v FROM t", 40)
	enginetest.TransactionEnd(t, holder, tx, false)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
	enginetest.LockEmpty(t, e)
}
func TestMergeNothingRowRecheck(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)", "CREATE TABLE source(id INT,v INT)", "INSERT INTO source VALUES(1,7)"} {
		enginetest.SessionSQL(t, peer, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "UPDATE t SET v=40")
	done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED AND t.v=40 AND s.v=7 THEN DO NOTHING WHEN MATCHED THEN UPDATE SET v=t.v+s.v RETURNING merge_action(),t.v")
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForNoKeyUpdate)
	enginetest.SessionSQL(t, peer, "UPDATE source SET v=99")
	enginetest.TransactionEnd(t, holder, tx, true)
	mergeNothingPendingOutput(t, done, 0, nil)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 40)
	enginetest.TransactionRows(t, peer, "SELECT v FROM source", 99)
	enginetest.LockEmpty(t, e)
}
func TestMergeNothingSourceAfter(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)", "CREATE TABLE source(id INT,v INT)", "INSERT INTO source VALUES(1,7)"} {
		enginetest.SessionSQL(t, peer, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "UPDATE t SET id=2,v=40")
	done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED BY SOURCE THEN DO NOTHING WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.v) RETURNING merge_action(),t.id,t.v")
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForNoKeyUpdate)
	enginetest.SessionSQL(t, peer, "UPDATE source SET v=99")
	enginetest.TransactionEnd(t, holder, tx, true)
	mergeNothingPendingOutput(t, done, 1, [][]driver.Value{{"INSERT", int64(1), int64(7)}})
	if got := enginetest.Tuples(enginetest.SessionSQL(t, peer, "SELECT id,v FROM t ORDER BY id")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(7)}, {int64(2), int64(40)}}) {
		t.Fatalf("survivors got %v", got)
	}
	enginetest.LockEmpty(t, e)
}
func TestMergeNothingCancellation(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT UNIQUE,v INT)", "INSERT INTO t VALUES(3,30)", "CREATE TABLE source(id INT)", "INSERT INTO source VALUES(1),(2),(3)"} {
		enginetest.SessionSQL(t, peer, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "UPDATE t SET v=99")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ast := enginetest.Statement(t, "MERGE INTO t USING (SELECT id FROM source ORDER BY id) s ON t.id=s.id WHEN NOT MATCHED AND s.id=1 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT VALUES(s.id,20) WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING merge_action(),t.id")
	type completion struct {
		result core.Result
		err    error
		scope  *core.StatementScope
	}
	done := make(chan completion, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		op, err := worker.Acquire(ctx)
		var result core.Result
		var scope *core.StatementScope
		if err == nil {
			scope, err = e.StartStatement(op)
			if err == nil {
				result, err = scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
				if err != nil && result.Rows != nil {
					result.Rows.Close()
					result = core.Result{}
				}
			}
			op.Done()
		}
		done <- completion{result, err, scope}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("cancelled omission worker did not retire")
		}
	})
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForNoKeyUpdate)
	if got := enginetest.Tuples(enginetest.SessionSQL(t, peer, "SELECT id,v FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(3), int64(30)}}) {
		t.Fatalf("pending survivors got %v", got)
	}
	cancel()
	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) || out.result.Rows != nil || out.result.Count != 0 {
			t.Fatal("cancel publication", out.result, out.err)
		}
		if out.scope == nil || out.scope.View != nil || out.scope.LockTicket != nil || !out.scope.Finished {
			t.Fatal("cancel ownership", out.scope)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending omission cancellation did not finish")
	}
	enginetest.TransactionEnd(t, holder, tx, false)
	if got := enginetest.Tuples(enginetest.SessionSQL(t, peer, "SELECT id,v FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(3), int64(30)}}) {
		t.Fatalf("cancelled survivors got %v", got)
	}
	enginetest.LockEmpty(t, e)
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20)")
	enginetest.SessionSQL(t, peer, "UPDATE t SET v=v+1")
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 21, 31)
	enginetest.LockEmpty(t, e)
}
