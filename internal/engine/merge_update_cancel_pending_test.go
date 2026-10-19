package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func TestMergeUpdateMixedPendingCancellation(t *testing.T) {
	mergeUpdatePendingCancellation(t, false)
}

func mergeUpdatePendingCancellation(t *testing.T, targetOnly bool) {
	e := New()
	holder := sessionStart(t, e, nil)
	worker := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	row := e.storage.relations[ref.Address].versions[0].row
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "UPDATE t SET v=20")
	if targetOnly {
		sessionSQL(t, worker, "BEGIN")
		sessionSQL(t, worker, "INSERT INTO t VALUES(9,90)")
		sessionSQL(t, worker, "SAVEPOINT before_cancel")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := "MERGE INTO t USING (VALUES(2),(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
	if targetOnly {
		q = "MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
	}
	ast := statement(t, q)
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
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.transactions.ticket, lock.ForNoKeyUpdate)
	transactionRows(t, peer, "SELECT v FROM t", 10)
	cancel()
	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) || out.result.Count != 0 || out.result.Rows != nil {
			t.Fatal("cancelled mixed result", out.result, out.err)
		}
		if out.scope == nil || out.scope.view != nil || out.scope.ticket != nil || out.scope.lockedDescriptor != nil || !out.scope.finished {
			t.Fatal("Finish retained mixed pins/holds", out.scope)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mixed pending cancellation did not finish")
	}
	if targetOnly {
		sessionSQL(t, worker, "ROLLBACK TO before_cancel")
		resource := lock.Resource{ObjectRef: ref, Row: row}
		lockState := e.locks.Inspect(worker.transactions.ticket)
		entry := lockState.Resources[resource]
		held := entry != nil && entry.Holders[worker.transactions.ticket].Mode != 0
		_, journaled := lockState.Journals[worker.transactions.ticket][resource]
		pending := false
		if entry != nil {
			for _, request := range entry.Pending {
				pending = pending || !request.Terminal && request.Ticket.Ref().Session() == worker.token
			}
		}
		if held || journaled || pending {
			t.Fatal("savepoint retained canceled row contribution", held, journaled, pending)
		}
		transactionRows(t, worker, "SELECT v FROM t ORDER BY id", 10, 90)
		sessionSQL(t, worker, "RELEASE before_cancel")
		sessionSQL(t, worker, "ROLLBACK")
	}
	transactionEnd(t, holder, tx, false)
	transactionRows(t, peer, "SELECT v FROM t", 10)
	lockEmpty(t, e)
	sessionSQL(t, peer, "INSERT INTO t VALUES(2,30)") // reuse accepted-prefix unique claim
	sessionSQL(t, peer, "UPDATE t SET v=v+1")         // reuse the exact row hold
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 31)
	lockEmpty(t, e)
}
