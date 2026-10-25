package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestInsertSelectCancellationBeforeFinish(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		e := engine.New()
		worker, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, worker, "CREATE TABLE t(i INT)")
		enginetest.SessionSQL(t, worker, "INSERT INTO t VALUES(9)")
		enginetest.SessionSQL(t, worker, "CREATE TABLE s(i INT)")
		enginetest.SessionSQL(t, worker, "INSERT INTO s VALUES(1),(2)")
		var tx ids.TxRef
		if explicit {
			tx = enginetest.TransactionBegin(t, worker)
			enginetest.SessionSQL(t, worker, "INSERT INTO t VALUES(7)")
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
		result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO t SELECT i FROM s"), 0, nil)
		if err != nil || result.Count != 2 || result.Rows != nil {
			t.Fatal(result, err)
		}
		enginetest.TransactionRows(t, observer, "SELECT i FROM t", 9)
		// Synchronize at the real unpublished stage: cancellation vetoes Finish even
		// after the complete batch was assembled and Apply succeeded once.
		cancel()
		if err = scope.Finish(nil); err != context.Canceled {
			t.Fatal(err)
		}
		op.Done()
		enginetest.TransactionRows(t, observer, "SELECT i FROM t", 9)
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
			enginetest.TransactionEnd(t, worker, tx, false)
		}
		enginetest.LockEmpty(t, e)
	}
}
