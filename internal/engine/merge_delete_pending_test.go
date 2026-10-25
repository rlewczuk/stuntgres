package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func TestMergeDeleteConcurrentClasses(t *testing.T) {
	for _, lane := range []string{"matchedLosesOn", "bySourceGainsOn", "noWiden"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				e := New()
				holder := sessionStart(t, e, nil)
				worker := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
				sessionSQL(t, peer, "CREATE TABLE source(id INT,v INT)")
				sessionSQL(t, peer, "INSERT INTO source VALUES(1,10)")
				seed, hold := "INSERT INTO t VALUES(1,10)", "UPDATE t SET v=20 WHERE id=1"
				if lane == "bySourceGainsOn" {
					seed, hold = "INSERT INTO t VALUES(1,20)", "UPDATE t SET v=10 WHERE id=1"
				}
				if lane == "noWiden" {
					seed = "INSERT INTO t VALUES(1,10),(3,30)"
				}
				sessionSQL(t, peer, seed)
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				row := e.Storage.Relations[ref.Address].Versions[0].Row
				tx := transactionBegin(t, holder)
				sessionSQL(t, holder, hold)
				q := "MERGE INTO t USING source s ON t.id=s.id AND t.v=s.v WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.v+60)"
				if lane == "bySourceGainsOn" {
					q = "MERGE INTO t USING source s ON t.id=s.id AND t.v=s.v WHEN MATCHED THEN UPDATE SET v=99 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,s.v+60)"
				}
				if lane == "noWiden" {
					q = "MERGE INTO t USING source s ON t.v=s.v WHEN MATCHED THEN DELETE"
				}
				done := mergeUpdatePendingExecute(t, worker, q)
				mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
				sessionSQL(t, peer, "UPDATE source SET v=30")
				sessionSQL(t, peer, "COMMENT ON TABLE t IS 'compatible while MERGE DELETE waits'")
				if lane == "noWiden" {
					sessionSQL(t, peer, "UPDATE t SET v=10 WHERE id=3")
				}
				transactionEnd(t, holder, tx, commit)
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
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 20, 10)
					} else {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10)
					}
				} else if lane == "matchedLosesOn" && !commit {
					transactionRows(t, peer, "SELECT v FROM t")
				} else {
					transactionRows(t, peer, "SELECT v FROM t", 70)
				}
				lockEmpty(t, e)
				sessionSQL(t, peer, "INSERT INTO t VALUES(8,80)")
				sessionSQL(t, peer, "DELETE FROM t WHERE id=8")
				lockEmpty(t, e)
			})
		}
	}
}

func TestMergeDeleteMixedPendingCancellation(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	worker := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
	sessionSQL(t, peer, "CREATE TABLE source(ord INT,id INT)")
	sessionSQL(t, peer, "INSERT INTO source VALUES(1,1),(2,2),(3,4),(4,3)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[2].Row
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "UPDATE t SET v=99 WHERE id=3")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ast := statement(t, "MERGE INTO t USING (SELECT * FROM source ORDER BY ord) s ON t.id<=s.id AND t.id>=s.id WHEN MATCHED AND s.id=2 THEN UPDATE SET v=21 WHEN MATCHED THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,40)")
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
			t.Error("DELETE worker failed to retire")
		}
	})
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
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
	transactionEnd(t, holder, tx, false)
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
	lockEmpty(t, e)
	sessionSQL(t, peer, "INSERT INTO t VALUES(4,40)")
	sessionSQL(t, peer, "UPDATE t SET v=v+1")
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 21, 31, 41)
	lockEmpty(t, e)
}

func TestMergeDeleteRowLocks(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	worker := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	row := e.Storage.Relations[ref.Address].Versions[0].Row
	tx := transactionBegin(t, holder)
	sessionSQL(t, holder, "SELECT id FROM t FOR KEY SHARE")
	done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE")
	mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
	transactionRows(t, peer, "SELECT v FROM t", 10)
	transactionEnd(t, holder, tx, false)
	mergeUpdatePendingOutcome(t, done, 1, nil)
	transactionRows(t, peer, "SELECT v FROM t")
	lockEmpty(t, e)
}

func TestMergeDeleteRestartRowLocks(t *testing.T) {
	for _, bySource := range []bool{false, true} {
		t.Run(map[bool]string{false: "matched", true: "bySource"}[bySource], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			worker := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			row := e.Storage.Relations[ref.Address].Versions[0].Row
			a := transactionBegin(t, holder)
			locked := sessionSQL(t, holder, "SELECT id FROM t FOR KEY SHARE")
			locked.Rows.Close()
			b := transactionBegin(t, writer)
			sessionSQL(t, writer, "UPDATE t SET v=20")
			q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND t.v=20 THEN DELETE WHEN MATCHED THEN UPDATE SET v=t.v+1"
			if bySource {
				q = "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id AND t.v=10 WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN DELETE"
			}
			done := mergeUpdatePendingExecute(t, worker, q)
			// The originally selected nonkey UPDATE requests NKU. Following the
			// committed predecessor upgrades before either DELETE arm is selected.
			mergeUpdatePendingRow(t, e, ref, row, worker, writer.Transactions.Ticket, lock.ForNoKeyUpdate)
			transactionEnd(t, writer, b, true)
			mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
			transactionRows(t, peer, "SELECT v FROM t", 20)
			transactionEnd(t, holder, a, false)
			mergeUpdatePendingOutcome(t, done, 1, nil)
			transactionRows(t, peer, "SELECT v FROM t")
			lockEmpty(t, e)
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,30)")
			sessionSQL(t, peer, "DELETE FROM t")
			lockEmpty(t, e)
		})
	}
}
