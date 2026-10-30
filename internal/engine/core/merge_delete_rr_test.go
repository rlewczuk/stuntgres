package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

func TestMergeDeleteRepeatableReadLineage(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		merge           bool
	}{
		{"unconditional", "", false},
		{"original10Only", " AND t.v=10", false},
		{"own100Only", " AND t.v=100", false},
		{"mergeUnconditional", "", true},
		{"mergeOriginal10Only", " AND t.v=10", true},
		{"mergeOwn100Only", " AND t.v=100", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			worker := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			original := e.Storage.Relations[ref.Address].Versions[0]
			enginetest.SessionSQL(t, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			enginetest.TransactionRows(t, worker, "SELECT v FROM t WHERE id=1", 10)
			frontier := worker.Transactions.Frontier
			enginetest.SessionSQL(t, worker, "UPDATE t SET v=21 WHERE id=2")
			initial := "UPDATE t SET v=100 WHERE id=1"
			if tc.merge {
				initial = "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100"
			}
			enginetest.SessionSQL(t, worker, initial)
			worker.Transactions.Mu.Lock()
			replacementBase := original.Version
			found := false
			for _, own := range worker.Transactions.Writes[ref.Address] {
				if own.Row == original.Row {
					replacementBase = own.Base
					found = own.Version != original.Version
				}
			}
			worker.Transactions.Mu.Unlock()
			if !found || replacementBase != original.Version {
				t.Fatal("own replacement lost snapshot predecessor", replacementBase, original.Version)
			}
			enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 100, 21)
			enginetest.SessionSQL(t, worker, "SAVEPOINT deletion")
			op := enginetest.TransactionOperation(t, worker)
			result, err := e.Execute(op, enginetest.Statement(t, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED"+tc.condition+" THEN DELETE"), 0, nil)
			op.Done()
			n := int64(1)
			if tc.condition == " AND t.v=10" {
				n = 0
			}
			if err != nil || result.Count != n {
				t.Fatal("RR own-head deletion", result, err, n)
			}
			if worker.Transactions.Frontier != frontier {
				t.Fatal("deletion replaced pinned RR frontier")
			}
			if n == 1 {
				enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 21)
			} else {
				enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 100, 21)
			}
			enginetest.SessionSQL(t, worker, "ROLLBACK TO deletion")
			enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 100, 21)
			if worker.Transactions.Frontier != frontier {
				t.Fatal("savepoint recovery replaced pinned RR frontier")
			}
			worker.Transactions.Mu.Lock()
			restored := false
			for _, own := range worker.Transactions.Writes[ref.Address] {
				if own.Row == original.Row && own.Base == original.Version && own.Version != original.Version {
					restored = true
				}
			}
			worker.Transactions.Mu.Unlock()
			if !restored {
				t.Fatal("savepoint recovery lost own replacement provenance")
			}
			enginetest.SessionSQL(t, worker, "RELEASE deletion")
			enginetest.SessionSQL(t, worker, "ROLLBACK")
			enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
			enginetest.LockEmpty(t, e)
		})
	}
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "peerDeleteRollback", true: "peerDeleteCommit"}[commit], func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			row := e.Storage.Relations[ref.Address].Versions[0].Row
			enginetest.SessionSQL(t, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			enginetest.TransactionRows(t, worker, "SELECT v FROM t", 10)
			tx := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "DELETE FROM t")
			done := mergeUpdatePendingExecute(t, worker, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE")
			mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
			enginetest.TransactionEnd(t, holder, tx, commit)
			if commit {
				mergeUpdatePendingOutcome(t, done, 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent delete"})
			} else {
				mergeUpdatePendingOutcome(t, done, 1, nil)
				enginetest.TransactionRows(t, worker, "SELECT v FROM t")
			}
			enginetest.SessionSQL(t, worker, "ROLLBACK")
			if commit {
				enginetest.TransactionRows(t, peer, "SELECT v FROM t")
			} else {
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
