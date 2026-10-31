package executor_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

func TestMergeSourceUpdatePending(t *testing.T) {
	t.Run("repeatableRead", func(t *testing.T) { mergeUpdateRepeatableReadPending(t, true) })
	for _, lane := range []string{"retainsClass", "noWiden", "twoActions", "fixedSource", "insertFault", "delete", "deleteNoInsert", "deleteDefault", "deleteNotNull", "deleteUnique"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				e := engine.New()
				holder := enginetest.StartSession(t, e, nil)
				worker := enginetest.StartSession(t, e, nil)
				peer := enginetest.StartSession(t, e, nil)
				ddl := "CREATE TABLE t(id INT,v INT)"
				if lane == "deleteNotNull" {
					ddl = "CREATE TABLE t(id INT NOT NULL,v INT)"
				}
				if lane == "deleteUnique" {
					ddl = "CREATE TABLE t(id INT CONSTRAINT t_key UNIQUE,v INT)"
				}
				if lane == "deleteDefault" {
					ddl = "CREATE TABLE t(id INT DEFAULT 8,v INT DEFAULT 80)"
				}
				enginetest.SessionSQL(t, peer, ddl)
				matched := lane == "twoActions" || lane == "fixedSource" || lane == "insertFault"
				seed := "INSERT INTO t VALUES(2,10)"
				hold := "UPDATE t SET id=1,v=20"
				if matched {
					seed = "INSERT INTO t VALUES(1,10)"
					hold = "UPDATE t SET id=2"
				}
				if len(lane) >= 6 && lane[:6] == "delete" {
					hold = "DELETE FROM t"
				}
				enginetest.SessionSQL(t, peer, seed)
				enginetest.SessionSQL(t, peer, "CREATE TABLE source(id INT,x INT)")
				enginetest.SessionSQL(t, peer, "INSERT INTO source VALUES(1,7)")
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				row := e.Storage.Relations[ref.Address].Versions[0].Row
				tx := enginetest.TransactionBegin(t, holder)
				enginetest.SessionSQL(t, holder, hold)
				q := "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=999 WHEN NOT MATCHED BY SOURCE AND t.v>=20 THEN UPDATE SET v=100 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=200"
				if matched {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.x)"
				}
				if lane == "insertFault" {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,1/(s.x-7))"
				}
				if len(lane) >= 6 && lane[:6] == "delete" {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100"
					if lane == "deleteDefault" {
						q += " WHEN NOT MATCHED THEN INSERT DEFAULT VALUES"
					} else if lane == "deleteUnique" {
						q += " WHEN NOT MATCHED THEN INSERT VALUES(COALESCE(s.id,1),s.x)"
					} else if lane != "deleteNoInsert" {
						q += " WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.x)"
					}
				}
				done := mergeUpdatePendingExecute(t, worker, q)
				enginetest.RowLockPending(t, e, ref, row, worker, holder.Transactions.Ticket, 0)
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
				if lane == "fixedSource" {
					enginetest.SessionSQL(t, peer, "UPDATE source SET x=8")
				}
				if lane == "noWiden" {
					enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(3,30)")
				}
				enginetest.TransactionEnd(t, holder, tx, commit)
				count := int64(1)
				var diagnostic *sqlstate.Error
				if matched && commit {
					count = 2
				}
				if len(lane) >= 6 && lane[:6] == "delete" && lane != "deleteNoInsert" {
					count = 2
				}
				if lane == "deleteNoInsert" && commit {
					count = 0
				}
				if lane == "insertFault" && commit {
					count = 0
					diagnostic = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
				}
				if lane == "deleteNotNull" && commit {
					count = 0
					diagnostic = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "t", Column: "id"}
				}
				if lane == "deleteUnique" && commit {
					count = 0
					diagnostic = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "t_key"`, Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "t", Constraint: "t_key"}
				}
				mergeUpdatePendingOutcome(t, done, count, diagnostic)
				switch {
				case diagnostic != nil:
					if lane == "insertFault" {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t", 10)
					} else {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t")
					}
				case matched && commit:
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 7, 110)
				case matched:
					enginetest.TransactionRows(t, peer, "SELECT v FROM t", 1)
				case lane == "deleteDefault":
					if commit {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 80, 80)
					} else {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 110, 80)
					}
				case len(lane) >= 6 && lane[:6] == "delete":
					if commit {
						if lane == "deleteNoInsert" {
							enginetest.TransactionRows(t, peer, "SELECT v FROM t")
						} else {
							enginetest.TransactionRows(t, peer, "SELECT COALESCE(v,-1) FROM t ORDER BY id", 7, -1)
							enginetest.TransactionRows(t, peer, "SELECT count(*) FROM t WHERE id IS NULL AND v IS NULL", 1)
						}
					} else {
						if lane == "deleteNoInsert" {
							enginetest.TransactionRows(t, peer, "SELECT v FROM t", 110)
						} else {
							enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 7, 110)
						}
					}
				default:
					v := int64(200)
					if commit {
						v = 100
					}
					if lane == "noWiden" {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 30)
					} else {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t", v)
					}
				}
				enginetest.LockEmpty(t, e)
				enginetest.SessionSQL(t, peer, "UPDATE t SET v=v+1")
			})
		}
	}
}

func mergeSourceRepeatableReadFollowup(t *testing.T, e *core.Engine, s *session.Session) {
	t.Helper()
	q := "MERGE INTO t USING (SELECT 9 AS id WHERE FALSE) src ON t.id=src.id WHEN NOT MATCHED BY SOURCE AND t.id=1"
	enginetest.SessionSQL(t, s, "SAVEPOINT followup")
	op := enginetest.TransactionOperation(t, s)
	result, err := e.Execute(op, enginetest.Statement(t, q+" THEN UPDATE SET v=t.v+1"), 0, nil)
	op.Done()
	if err != nil || result.Count != 1 {
		t.Fatal(result, err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO followup")
	r := enginetest.SessionSQL(t, s, q+" AND t.v=10 THEN UPDATE SET v=t.v+1")
	if r.Count != 0 {
		t.Fatal(r.Count)
	}
	r = enginetest.SessionSQL(t, s, q+" AND t.v=100 THEN UPDATE SET v=t.v+1")
	if r.Count != 1 {
		t.Fatal(r.Count)
	}
	enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY id,v", 101, 21, 40)
	enginetest.SessionSQL(t, s, "ROLLBACK TO followup")
	enginetest.SessionSQL(t, s, "RELEASE followup")
}

func mergeUpdateRepeatableReadPending(t *testing.T, targetOnly bool) {
	for _, deleted := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "Update", true: "Delete"}[deleted]+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				e := engine.New()
				holder := enginetest.StartSession(t, e, nil)
				worker := enginetest.StartSession(t, e, nil)
				peer := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
				enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				original := e.Storage.Relations[ref.Address].Versions[0]
				enginetest.SessionSQL(t, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				enginetest.TransactionRows(t, worker, "SELECT v FROM t WHERE id=1", 10)
				worker.Transactions.Mu.Lock()
				frontier := worker.Transactions.Frontier
				roots := make(map[ids.ObjectAddress]visibility.Root, len(worker.Transactions.Roots))
				for address, root := range worker.Transactions.Roots {
					roots[address] = root
				}
				worker.Transactions.Mu.Unlock()
				enginetest.SessionSQL(t, worker, "UPDATE t SET v=21 WHERE id=2")
				enginetest.SessionSQL(t, worker, "DELETE FROM t WHERE id=3")
				enginetest.SessionSQL(t, worker, "INSERT INTO t VALUES(4,40)")
				worker.Transactions.Mu.Lock()
				priorWrites := append([]storage.RowVersion(nil), worker.Transactions.Writes[ref.Address]...)
				priorDeletes := worker.Transactions.Deletes[ref.Address]
				worker.Transactions.Mu.Unlock()
				if targetOnly {
					enginetest.SessionSQL(t, worker, "SAVEPOINT before_target_only")
				}
				tx := enginetest.TransactionBegin(t, holder)
				hold := "UPDATE t SET v=11 WHERE id=1"
				if deleted {
					hold = "DELETE FROM t WHERE id=1"
				}
				enginetest.SessionSQL(t, holder, hold)
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
				if targetOnly {
					q = "MERGE INTO t USING (SELECT 9 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND t.id=1 THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
				}
				done := mergeUpdatePendingExecute(t, worker, q)
				enginetest.RowLockPending(t, e, ref, original.Row, worker, holder.Transactions.Ticket, 0)
				enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
				enginetest.TransactionEnd(t, holder, tx, commit)
				if commit {
					action := "update"
					if deleted {
						action = "delete"
					}
					mergeUpdatePendingOutcome(t, done, 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent " + action})
					worker.Transactions.Mu.Lock()
					correctOverlay := len(worker.Transactions.Writes) == 0 && len(worker.Transactions.Deletes) == 0
					if targetOnly {
						// The target-only lane has a recoverable savepoint: its
						// successful ancestor work survives the failed statement.
						correctOverlay = reflect.DeepEqual(worker.Transactions.Writes[ref.Address], priorWrites) && worker.Transactions.Deletes[ref.Address] == priorDeletes
					}
					worker.Transactions.Mu.Unlock()
					if !correctOverlay {
						t.Fatal("serialization violated private recovery boundary")
					}
					op := enginetest.TransactionOperation(t, worker)
					_, err := e.Execute(op, enginetest.Statement(t, "SELECT * FROM t"), 0, nil)
					op.Done()
					var got *sqlstate.Error
					if !errors.As(err, &got) || got.Code != "25P02" {
						t.Fatal("missing aborted transaction", err)
					}
					if targetOnly {
						enginetest.SessionSQL(t, worker, "ROLLBACK TO before_target_only")
						enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY id", 10, 21, 40)
						enginetest.SessionSQL(t, worker, "RELEASE before_target_only")
					}
				} else {
					mergeUpdatePendingOutcome(t, done, 1, nil)
					worker.Transactions.Mu.Lock()
					sameSnapshot := worker.Transactions.SnapshotSet && worker.Transactions.Frontier == frontier && reflect.DeepEqual(worker.Transactions.Roots, roots)
					worker.Transactions.Mu.Unlock()
					if !sameSnapshot {
						t.Fatal("MERGE advanced or replaced the pinned RR snapshot")
					}
					enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 100, 21, 40)
					// A rolled-back peer leaves the original logical row and
					// snapshot predecessor authoritative for the own replacement.
					worker.Transactions.Mu.Lock()
					var own storage.RowVersion
					for _, v := range worker.Transactions.Writes[ref.Address] {
						if v.Row == original.Row {
							own = v
						}
					}
					worker.Transactions.Mu.Unlock()
					if own.Row != original.Row || own.Version == original.Version || own.Base == 0 {
						t.Fatal("lost replacement identity/predecessor", own, original)
					}
					if own.Base != original.Version {
						t.Fatal("rollback failed to retain original predecessor", own)
					}
					if targetOnly {
						mergeSourceRepeatableReadFollowup(t, e, worker)
					}
				}
				enginetest.SessionSQL(t, worker, "ROLLBACK")
				if commit && deleted {
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 20, 30)
				} else {
					v := int64(10)
					if commit {
						v = 11
					}
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 20, 30)
				}
				enginetest.LockEmpty(t, e)
				enginetest.SessionSQL(t, peer, "UPDATE t SET v=v+1")
			})
		}
	}
}

// These are executor/Finish and manager-state witnesses, not public SQL copies.
// A live request on the original RowID establishes the exact interleaving.
func mergeUpdatePendingExecute(t *testing.T, s *session.Session, q string) <-chan deleteOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ast := enginetest.Statement(t, q)
	done := make(chan deleteOutcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		op, err := s.Acquire(ctx)
		var result core.Result
		if err == nil {
			result, err = core.EngineOf(s).Execute(op, ast, 0, nil)
			op.Done()
		}
		done <- deleteOutcome{result, err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("MERGE worker failed to retire")
		}
	})
	return done
}

func mergeUpdatePendingOutcome(t *testing.T, done <-chan deleteOutcome, count int64, diagnostic *sqlstate.Error) {
	t.Helper()
	select {
	case out := <-done:
		if out.result.Rows != nil || out.result.Count != count {
			t.Fatal("MERGE result", out.result, count)
		}
		if diagnostic == nil {
			if out.err != nil {
				t.Fatal(out.err)
			}
		} else {
			var got *sqlstate.Error
			if !errors.As(out.err, &got) || !reflect.DeepEqual(*got, *diagnostic) {
				t.Fatal("MERGE diagnostic", out.err, diagnostic)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MERGE did not finish")
	}
}

type deleteOutcome struct {
	result core.Result
	err    error
}
