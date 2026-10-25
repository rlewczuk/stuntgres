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

func TestMergeDeleteConcurrentClasses(t *testing.T) {
	for _, lane := range []string{"matchedLosesOn", "bySourceGainsOn", "noWiden"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				e := engine.New()
				holder := enginetest.StartSession(t, e, nil)
				worker := enginetest.StartSession(t, e, nil)
				peer := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
				enginetest.SessionSQL(t, peer, "CREATE TABLE source(id INT,v INT)")
				enginetest.SessionSQL(t, peer, "INSERT INTO source VALUES(1,10)")
				seed, hold := "INSERT INTO t VALUES(1,10)", "UPDATE t SET v=20 WHERE id=1"
				if lane == "bySourceGainsOn" {
					seed, hold = "INSERT INTO t VALUES(1,20)", "UPDATE t SET v=10 WHERE id=1"
				}
				if lane == "noWiden" {
					seed = "INSERT INTO t VALUES(1,10),(3,30)"
				}
				enginetest.SessionSQL(t, peer, seed)
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				row := e.Storage.Relations[ref.Address].Versions[0].Row
				tx := enginetest.TransactionBegin(t, holder)
				enginetest.SessionSQL(t, holder, hold)
				q := "MERGE INTO t USING source s ON t.id=s.id AND t.v=s.v WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.v+60)"
				if lane == "bySourceGainsOn" {
					q = "MERGE INTO t USING source s ON t.id=s.id AND t.v=s.v WHEN MATCHED THEN UPDATE SET v=99 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.v+60)"
				}
				if lane == "noWiden" {
					q = "MERGE INTO t USING source s ON t.v=s.v WHEN MATCHED THEN DELETE"
				}
				done := mergeUpdatePendingExecute(t, worker, q)
				mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
				enginetest.SessionSQL(t, peer, "UPDATE source SET v=30")
				enginetest.SessionSQL(t, peer, "COMMENT ON TABLE t IS 'compatible while MERGE DELETE waits'")
				if lane == "noWiden" {
					enginetest.SessionSQL(t, peer, "UPDATE t SET v=10 WHERE id=3")
				}
				enginetest.TransactionEnd(t, holder, tx, commit)
				n := int64(2)
				if lane == "matchedLosesOn" && !commit {
					n = 1
				}
				if lane == "noWiden" {
					n = 1
					if commit {
						n = 0
					}
				}
				mergeUpdatePendingOutcome(t, done, n, nil)
				if lane == "noWiden" {
					if commit {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 20, 10)
					} else {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10)
					}
				} else if lane == "matchedLosesOn" && !commit {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t")
				} else {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t", 70)
				}
				enginetest.LockEmpty(t, e)
				enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(8,80)")
				enginetest.SessionSQL(t, peer, "DELETE FROM t WHERE id=8")
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestMergeDeleteMixedPendingCancellation(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
	enginetest.SessionSQL(t, peer, "CREATE TABLE source(ord INT,id INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO source VALUES(1,1),(2,2),(3,4),(4,3)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[2].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "UPDATE t SET v=99 WHERE id=3")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ast := enginetest.Statement(t, "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id<=s.id AND t.id>=s.id WHEN MATCHED AND s.id=2 THEN UPDATE SET v=21 WHEN MATCHED THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,40)")
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
			t.Error("DELETE worker failed to retire")
		}
	})
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
	cancel()
	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) || out.result.Count != 0 || out.result.Rows != nil {
			t.Fatal("cancelled mixed DELETE", out.result, out.err)
		}
		if out.scope == nil || out.scope.View != nil || out.scope.LockTicket != nil || out.scope.LockedStorage != nil || !out.scope.Finished {
			t.Fatal("Finish retained DELETE authority", out.scope)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending DELETE cancellation failed")
	}
	enginetest.TransactionEnd(t, holder, tx, false)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
	enginetest.LockEmpty(t, e)
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(4,40)")
	enginetest.SessionSQL(t, peer, "UPDATE t SET v=v+1")
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 21, 31, 41)
	enginetest.LockEmpty(t, e)
}

func TestMergeDeleteRowLocks(t *testing.T) {
	e := engine.New()
	holder := enginetest.StartSession(t, e, nil)
	worker := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := enginetest.TransactionBegin(t, holder)
	enginetest.SessionSQL(t, holder, "SELECT id FROM t FOR KEY SHARE")
	done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE")
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
	enginetest.TransactionEnd(t, holder, tx, false)
	mergeUpdatePendingOutcome(t, done, 1, nil)
	enginetest.TransactionRows(t, peer, "SELECT v FROM t")
	enginetest.LockEmpty(t, e)
}

func TestMergeDeleteRestartRowLocks(t *testing.T) {
	for _, bySource := range []bool{false, true} {
		t.Run(map[bool]string{false: "matched", true: "bySource"}[bySource], func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			writer := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			row := e.Storage.Relations[ref.Address].Versions[0].Row
			a := enginetest.TransactionBegin(t, holder)
			locked := enginetest.SessionSQL(t, holder, "SELECT id FROM t FOR KEY SHARE")
			locked.Rows.Close()
			b := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "UPDATE t SET v=20")
			q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND t.v=20 THEN DELETE WHEN MATCHED THEN UPDATE SET v=t.v+1"
			if bySource {
				q = "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id AND t.v=10 WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN DELETE"
			}
			done := mergeUpdatePendingExecute(t, worker, q)
			// The originally selected nonkey UPDATE requests NKU. Following the
			// committed predecessor upgrades before either DELETE arm is selected.
			mergeUpdatePendingRow(t, e, ref, row, worker, writer.Transactions.Ticket, lock.ForNoKeyUpdate)
			enginetest.TransactionEnd(t, writer, b, true)
			mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
			enginetest.TransactionRows(t, peer, "SELECT v FROM t", 20)
			enginetest.TransactionEnd(t, holder, a, false)
			mergeUpdatePendingOutcome(t, done, 1, nil)
			enginetest.TransactionRows(t, peer, "SELECT v FROM t")
			enginetest.LockEmpty(t, e)
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,30)")
			enginetest.SessionSQL(t, peer, "DELETE FROM t")
			enginetest.LockEmpty(t, e)
		})
	}
}
