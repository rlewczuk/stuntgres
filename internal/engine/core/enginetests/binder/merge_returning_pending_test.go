package binder_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestMergeReturningCancellation(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT UNIQUE,v INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "UPDATE t SET v=40")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ast := enginetest.Statement(t, "MERGE INTO t USING (VALUES(2),(1)) src(id) ON t.id=src.id WHEN MATCHED THEN UPDATE SET v=99 WHEN NOT MATCHED THEN INSERT VALUES(src.id,20) RETURNING merge_action(),old.v,new.v")
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
			t.Error("cancelled returning worker did not retire")
		}
	})
	enginetest.RowLockPending(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForNoKeyUpdate)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
	cancel()
	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) || out.result.Rows != nil || out.result.Count != 0 {
			t.Fatal("cancel exposed provisional output", out.result, out.err)
		}
		if out.scope == nil || out.scope.View != nil || out.scope.LockTicket != nil || !out.scope.Finished {
			t.Fatal("cancel retained owner", out.scope)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending cancellation did not finish")
	}
	enginetest.TransactionEnd(t, holder, tx, false)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
	enginetest.LockEmpty(t, e)
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(2,20)")
	enginetest.SessionSQL(t, peer, "UPDATE t SET v=v+1")
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 21)
	enginetest.LockEmpty(t, e)
}
