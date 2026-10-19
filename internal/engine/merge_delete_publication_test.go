package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"testing"
)

func TestMergeDeletePublication(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "cancelBeforeFinish"}[cancelled], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
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
			result, err := scope.Execute(statement(t, "MERGE INTO t USING (VALUES(1),(2),(4)) s(id) ON t.id=s.id WHEN MATCHED AND s.id=1 THEN DELETE WHEN MATCHED THEN UPDATE SET v=21 WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED THEN INSERT VALUES(s.id,40)"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			if result.Count != 4 || result.Rows != nil {
				scope.Finish(errors.New("wrong aggregate count"))
				t.Fatal(result)
			}
			transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
			if cancelled {
				cancel()
				if err = scope.Finish(nil); !errors.Is(err, context.Canceled) {
					t.Fatal("Finish lost cancellation", err)
				}
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
			} else {
				if err = scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 21, 40)
			}
			if scope.view != nil || scope.ticket != nil || scope.lockedStorage != nil || !scope.finished {
				t.Fatal("Finish retained DELETE resources")
			}
			lockEmpty(t, e)
		})
	}
}

func TestMergeDeleteLineage(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			original := e.storage.Relations[ref.Address].Versions[0]
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "UPDATE t SET v=11 WHERE id=1")
			sessionSQL(t, s, "INSERT INTO t VALUES(2,20)")
			sessionSQL(t, s, "SAVEPOINT keep")
			sessionSQL(t, s, "MERGE INTO t USING (VALUES(1),(2)) src(id) ON t.id=src.id WHEN MATCHED THEN DELETE")
			transactionRows(t, s, "SELECT v FROM t")
			transactionRows(t, peer, "SELECT v FROM t", 10)
			sessionSQL(t, s, "ROLLBACK TO keep")
			transactionRows(t, s, "SELECT v FROM t ORDER BY id", 11, 20)
			sessionSQL(t, s, "MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) src ON t.id=src.id WHEN NOT MATCHED BY SOURCE THEN DELETE")
			transactionEnd(t, s, tx, commit)
			if commit {
				transactionRows(t, peer, "SELECT v FROM t")
				if e.storage.Relations[ref.Address].Ends == nil {
					t.Fatal("no canonical deletion root")
				}
			} else {
				transactionRows(t, peer, "SELECT v FROM t", 10)
				head := e.storage.Relations[ref.Address].Versions[0]
				if head.Row != original.Row || head.Version != original.Version {
					t.Fatal("rollback reminted predecessor")
				}
			}
			lockEmpty(t, e)
		})
	}
	for _, fault := range []string{"ordinal", "version", "storage", "ref", "copiedStage"} {
		t.Run(fault, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(statement(t, "MERGE INTO t USING (VALUES(1)) src(id) ON t.id=src.id WHEN MATCHED THEN DELETE"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			var candidate visibility.DeleteCandidate
			for _, root := range scope.stage.deletes {
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
			transactionRows(t, peer, "SELECT v FROM t", 10)
			lockEmpty(t, e)
		})
	}
}
