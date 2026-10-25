package core_test

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

func TestMergeUpdateMixedPendingCancellation(t *testing.T) {
	mergeUpdatePendingCancellation(t, false)
}

func mergeUpdatePendingCancellation(t *testing.T, targetOnly bool) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "UPDATE t SET v=20")
	if targetOnly {
		enginetest.SessionSQL(t, worker, "BEGIN")
		enginetest.SessionSQL(t, worker, "INSERT INTO t VALUES(9,90)")
		enginetest.SessionSQL(t, worker, "SAVEPOINT before_cancel")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := "MERGE INTO t USING (VALUES(2),(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
	if targetOnly {
		q = "MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
	}
	ast := enginetest.Statement(t, q)
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
			t.Error("cancelled mixed worker did not retire")
		}
	})
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForNoKeyUpdate)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
	cancel()
	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) || out.result.Count != 0 || out.result.Rows != nil {
			t.Fatal("cancelled mixed result", out.result, out.err)
		}
		if out.scope == nil || out.scope.View != nil || out.scope.LockTicket != nil || out.scope.LockedDescriptor != nil || !out.scope.Finished {
			t.Fatal("Finish retained mixed pins/holds", out.scope)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mixed pending cancellation did not finish")
	}
	if targetOnly {
		enginetest.SessionSQL(t, worker, "ROLLBACK TO before_cancel")
		resource := lock.Resource{ObjectRef: ref, Row: row}
		lockState := e.Locks.Inspect(worker.Transactions.Ticket)
		entry := lockState.Resources[resource]
		held := entry != nil && entry.Holders[worker.Transactions.Ticket].Mode != 0
		_, journaled := lockState.Journals[worker.Transactions.Ticket][resource]
		pending := false
		if entry != nil {
			for _, request := range entry.Pending {
				pending = pending || !request.Terminal && request.Ticket.Ref().Session() == worker.SessionToken
			}
		}
		if held || journaled || pending {
			t.Fatal("savepoint retained canceled row contribution", held, journaled, pending)
		}
		enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY id", 10, 90)
		enginetest.SessionSQL(t, worker, "RELEASE before_cancel")
		enginetest.SessionSQL(t, worker, "ROLLBACK")
	}
	enginetest.TransactionEnd(t, holder, tx, false)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
	enginetest.LockEmpty(t, e)
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(2,30)") // reuse accepted-prefix unique claim
	enginetest.SessionSQL(t, peer, "UPDATE t SET v=v+1")         // reuse the exact row hold
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 31)
	enginetest.LockEmpty(t, e)
}
