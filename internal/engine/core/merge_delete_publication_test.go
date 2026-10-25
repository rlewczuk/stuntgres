package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func TestMergeDeletePublication(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "cancelBeforeFinish"}[cancelled], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(enginetest.Statement(t, "MERGE INTO t USING (VALUES(1),(2),(4)) s(id) ON t.id=s.id WHEN MATCHED AND s.id=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=21 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,40)"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			if result.Count != 4 || result.Rows != nil {
				scope.Finish(errors.New("wrong aggregate count"))
				t.Fatal(result)
			}
			enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
			if cancelled {
				cancel()
				if err = scope.Finish(nil); !errors.Is(err, context.Canceled) {
					t.Fatal("Finish lost cancellation", err)
				}
				enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
			} else {
				if err = scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 21, 40)
			}
			if scope.View != nil || scope.LockTicket != nil || scope.LockedStorage != nil || !scope.Finished {
				t.Fatal("Finish retained DELETE resources")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestMergeDeleteLineage(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			original := e.Storage.Relations[ref.Address].Versions[0]
			tx := enginetest.TransactionBegin(t, s)
			enginetest.SessionSQL(t, s, "UPDATE t SET v=11 WHERE id=1")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(2,20)")
			enginetest.SessionSQL(t, s, "SAVEPOINT keep")
			enginetest.SessionSQL(t, s, "MERGE INTO t USING (VALUES(1),(2)) src(id) ON t.id=src.id WHEN MATCHED THEN DELETE")
			enginetest.TransactionRows(t, s, "SELECT v FROM t")
			enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
			enginetest.SessionSQL(t, s, "ROLLBACK TO keep")
			enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY id", 11, 20)
			enginetest.SessionSQL(t, s, "MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) src ON t.id=src.id WHEN NOT MATCHED BY SOURCE THEN DELETE")
			enginetest.TransactionEnd(t, s, tx, commit)
			if commit {
				enginetest.TransactionRows(t, peer, "SELECT v FROM t")
				if e.Storage.Relations[ref.Address].Ends == nil {
					t.Fatal("no canonical deletion root")
				}
			} else {
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
				head := e.Storage.Relations[ref.Address].Versions[0]
				if head.Row != original.Row || head.Version != original.Version {
					t.Fatal("rollback reminted predecessor")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
	for _, fault := range []string{"ordinal", "version", "storage", "ref", "copiedStage"} {
		t.Run(fault, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(enginetest.Statement(t, "MERGE INTO t USING (VALUES(1)) src(id) ON t.id=src.id WHEN MATCHED THEN DELETE"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			var candidate visibility.DeleteCandidate
			for _, root := range scope.Stage.Deletes {
				for _, c := range root.Rows {
					candidate = c
				}
			}
			if candidate.Row == 0 {
				scope.Finish(errors.New("missing candidate"))
				t.Fatal("no actual deletion candidate")
			}
			switch fault {
			case "ordinal":
				candidate.Ordinal++
			case "version":
				candidate.Version++
			case "storage":
				candidate.Storage = &storage.Relation{}
			case "ref":
				candidate.Ref.Epoch++
			case "copiedStage":
			}
			if fault == "copiedStage" {
				copy := *scope.Writes()
				_, err = copy.Delete(candidate)
			} else {
				_, err = scope.Writes().Delete(candidate)
			}
			if err == nil {
				scope.Finish(errors.New("accepted foreign deletion"))
				t.Fatal("accepted stale/copied provenance", fault)
			}
			scope.Finish(err)
			enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
			enginetest.LockEmpty(t, e)
		})
	}
}
