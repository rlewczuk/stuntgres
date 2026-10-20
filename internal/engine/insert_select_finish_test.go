package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestInsertSelectCancellationBeforeFinish(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		e := New()
		worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, worker, "CREATE TABLE t(i INT)")
		sessionSQL(t, worker, "INSERT INTO t VALUES(9)")
		sessionSQL(t, worker, "CREATE TABLE s(i INT)")
		sessionSQL(t, worker, "INSERT INTO s VALUES(1),(2)")
		var tx ids.TxRef
		if explicit {
			tx = transactionBegin(t, worker)
			sessionSQL(t, worker, "INSERT INTO t VALUES(7)")
		}
		ctx, cancel := context.WithCancel(context.Background())
		op, err := worker.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		result, err := scope.Execute(statement(t, "INSERT INTO t SELECT i FROM s"), 0, nil)
		if err != nil || result.Count != 2 || result.Rows != nil {
			t.Fatal(result, err)
		}
		transactionRows(t, observer, "SELECT i FROM t", 9)
		// Synchronize at the real unpublished stage: cancellation vetoes Finish even
		// after the complete batch was assembled and Apply succeeded once.
		cancel()
		if err = scope.Finish(nil); err != context.Canceled {
			t.Fatal(err)
		}
		op.Done()
		transactionRows(t, observer, "SELECT i FROM t", 9)
		worker.Transactions.Mu.Lock()
		state := worker.Transactions.State
		worker.Transactions.Mu.Unlock()
		want := session.TxIdle
		if explicit {
			want = session.TxAborted
		}
		if state != want {
			t.Fatal(state, want)
		}
		if explicit {
			transactionEnd(t, worker, tx, false)
		}
		lockEmpty(t, e)
	}
}
