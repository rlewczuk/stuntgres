package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func TestMergeReturningRowRecheck(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	worker := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)", "CREATE TABLE source(id INT,delta INT)", "INSERT INTO source VALUES(1,7)"} {
		sessionSQL(t, peer, q)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	row := e.storage.Relations[ref.Address].Versions[0].Row
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "UPDATE t SET v=40 WHERE id=1")
	done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING source src ON t.id=src.id WHEN MATCHED THEN UPDATE SET v=t.v+src.delta RETURNING merge_action(),src.delta,old.v,new.v")
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.transactions.ticket, lock.ForNoKeyUpdate)
	// The source changes only after observing the worker's actual row queue.
	sessionSQL(t, peer, "UPDATE source SET delta=99")
	transactionEnd(t, holder, tx, true)
	select {
	case out := <-done:
		if out.err != nil || out.result.Count != 1 || out.result.Rows == nil {
			t.Fatal(out.result, out.err)
		}
		defer out.result.Rows.Close()
		if got := tuples(out.result); !reflect.DeepEqual(got, [][]driver.Value{{"UPDATE", int64(7), int64(40), int64(47)}}) {
			t.Fatal("source replay/stale predecessor", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("returning row recheck did not retire")
	}
	transactionRows(t, peer, "SELECT v FROM t", 47)
	transactionRows(t, peer, "SELECT delta FROM source", 99)
	lockEmpty(t, e)
}
func TestMergeReturningCancellation(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	worker := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT UNIQUE,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	row := e.storage.Relations[ref.Address].Versions[0].Row
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "UPDATE t SET v=40")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ast := statement(t, "MERGE INTO t USING (VALUES(2),(1)) src(id) ON t.id=src.id WHEN MATCHED THEN UPDATE SET v=99 WHEN NOT MATCHED THEN INSERT VALUES(src.id,20) RETURNING merge_action(),old.v,new.v")
	type completion struct {
		result Result
		err    error
		scope  *StatementScope
	}
	done := make(chan completion, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		op, err := worker.Acquire(ctx)
		var result Result
		var scope *StatementScope
		if err == nil {
			scope, err = e.StartStatement(op)
			if err == nil {
				result, err = scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
				if err != nil && result.Rows != nil {
					result.Rows.Close()
					result = Result{}
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
			t.Error("cancelled returning worker did not retire")
		}
	})
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.transactions.ticket, lock.ForNoKeyUpdate)
	transactionRows(t, peer, "SELECT v FROM t", 10)
	cancel()
	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) || out.result.Rows != nil || out.result.Count != 0 {
			t.Fatal("cancel exposed provisional output", out.result, out.err)
		}
		if out.scope == nil || out.scope.view != nil || out.scope.ticket != nil || !out.scope.finished {
			t.Fatal("cancel retained owner", out.scope)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending cancellation did not finish")
	}
	transactionEnd(t, holder, tx, false)
	transactionRows(t, peer, "SELECT v FROM t", 10)
	lockEmpty(t, e)
	sessionSQL(t, peer, "INSERT INTO t VALUES(2,20)")
	sessionSQL(t, peer, "UPDATE t SET v=v+1")
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 21)
	lockEmpty(t, e)
}
