package engine

import (
	"errors"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestMergeDeleteRepeatableReadLineage(t *testing.T) {
	for _, condition := range []string{"", " AND t.v=10", " AND t.v=100"} {
		t.Run(condition, func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			worker := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			original := e.Storage.Relations[ref.Address].Versions[0]
			sessionSQL(t, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			transactionRows(t, worker, "SELECT v FROM t WHERE id=1", 10)
			frontier := worker.Transactions.Frontier
			sessionSQL(t, worker, "UPDATE t SET v=21 WHERE id=2")
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "UPDATE t SET v=11 WHERE id=1")
			done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100")
			mergeUpdatePendingRow(t, e, ref, original.Row, worker, holder.Transactions.Ticket, lock.ForNoKeyUpdate)
			transactionEnd(t, holder, tx, true)
			mergeUpdatePendingOutcome(t, done, 1, nil)
			transactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 10, 100, 21)
			sessionSQL(t, worker, "SAVEPOINT deletion")
			op := transactionOperation(t, worker)
			result, err := e.Execute(op, statement(t, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED"+condition+" THEN DELETE"), 0, nil)
			op.Done()
			if condition == "" {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != "21000" || d.Hint != "Ensure that not more than one source row matches any one target row." || result.Count != 0 {
					t.Fatal("RR duplicate deletion", result, err)
				}
				sessionSQL(t, worker, "ROLLBACK TO deletion")
			} else {
				n := int64(0)
				if condition == " AND t.v=100" {
					n = 1
				}
				if err != nil || result.Count != n {
					t.Fatal("RR own-head deletion", result, err, n)
				}
			}
			if worker.Transactions.Frontier != frontier {
				t.Fatal("deletion replaced pinned RR frontier")
			}
			if condition == " AND t.v=100" {
				transactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 10, 21)
			} else {
				transactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 10, 100, 21)
			}
			sessionSQL(t, worker, "ROLLBACK")
			transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 20)
			lockEmpty(t, e)
		})
	}
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "peerDeleteRollback", true: "peerDeleteCommit"}[commit], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			worker := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			row := e.Storage.Relations[ref.Address].Versions[0].Row
			sessionSQL(t, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			transactionRows(t, worker, "SELECT v FROM t", 10)
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "DELETE FROM t")
			done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE")
			mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
			transactionEnd(t, holder, tx, commit)
			if commit {
				mergeUpdatePendingOutcome(t, done, 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent delete"})
			} else {
				mergeUpdatePendingOutcome(t, done, 1, nil)
				transactionRows(t, worker, "SELECT v FROM t")
			}
			sessionSQL(t, worker, "ROLLBACK")
			if commit {
				transactionRows(t, peer, "SELECT v FROM t")
			} else {
				transactionRows(t, peer, "SELECT v FROM t", 10)
			}
			lockEmpty(t, e)
		})
	}
}
