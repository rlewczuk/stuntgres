package core_test

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

// These are executor/Finish and manager-state witnesses, not public SQL copies.
// A live request on the original RowID establishes the exact interleaving.
func mergeUpdatePendingRow(t *testing.T, e *core.Engine, ref ids.ObjectRef, row ids.RowID, worker *session.Session, blocker *lock.Ticket, mode lock.Mode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: row}]
		found := false
		if entry != nil && entry.Holders[blocker].Mode != 0 {
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == worker.SessionToken && (mode == 0 || request.Mode == mode) {
					found = true
					break
				}
			}
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("missing exact live MERGE row request", row, mode)
		}
		runtime.Gosched()
	}
}

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

func TestMergeUpdateReadCommittedPending(t *testing.T) {
	for _, lane := range []string{"restart", "keyChange", "keyChangeNoInsert", "delete", "deleteNoInsert", "fixedSource", "noWiden"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				e := engine.New()
				holder := enginetest.StartSession(t, e, nil)
				worker := enginetest.StartSession(t, e, nil)
				peer := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
				enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(3,30)")
				enginetest.SessionSQL(t, peer, "CREATE TABLE source(id INT,x INT)")
				enginetest.SessionSQL(t, peer, "INSERT INTO source VALUES(1,7)")
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				row := e.Storage.Relations[ref.Address].Versions[0].Row
				tx := enginetest.TransactionBegin(t, holder)
				hold := "UPDATE t SET v=20 WHERE id=1"
				if lane == "keyChange" || lane == "keyChangeNoInsert" {
					hold = "UPDATE t SET id=2 WHERE id=1"
				}
				if lane == "delete" || lane == "deleteNoInsert" {
					hold = "DELETE FROM t WHERE id=1"
				}
				enginetest.SessionSQL(t, holder, hold)
				q := "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED AND t.v>=20 THEN UPDATE SET v=100 WHEN MATCHED THEN UPDATE SET v=200"
				if lane == "fixedSource" {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.x"
				}
				if lane == "noWiden" {
					q = "MERGE INTO t USING source s ON t.v=10 WHEN MATCHED THEN UPDATE SET v=100"
				}
				if lane == "keyChange" || lane == "delete" {
					q += " WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
				}
				done := mergeUpdatePendingExecute(t, worker, q)
				mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, 0)
				enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 30)
				if lane == "fixedSource" {
					enginetest.SessionSQL(t, peer, "UPDATE source SET x=8")
				}
				if lane == "noWiden" {
					enginetest.SessionSQL(t, peer, "UPDATE t SET v=10 WHERE id=3")
				}
				enginetest.TransactionEnd(t, holder, tx, commit)
				n := int64(1)
				if commit && (lane == "deleteNoInsert" || lane == "keyChangeNoInsert" || lane == "noWiden") {
					n = 0
				}
				mergeUpdatePendingOutcome(t, done, n, nil)
				switch lane {
				case "restart":
					v := int64(200)
					if commit {
						v = 100
					}
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 30)
				case "keyChange":
					if commit {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 99, 10, 30)
					} else {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 200, 30)
					}
				case "keyChangeNoInsert":
					if commit {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 30)
					} else {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 200, 30)
					}
				case "delete":
					v := int64(200)
					if commit {
						v = 99
					}
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 30)
				case "deleteNoInsert":
					if commit {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 30)
					} else {
						enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 200, 30)
					}
				case "fixedSource":
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", 7, 30)
					enginetest.TransactionRows(t, peer, "SELECT x FROM source", 8)
				case "noWiden":
					v := int64(100)
					if commit {
						v = 20
					}
					enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 10)
				}
				enginetest.LockEmpty(t, e)
				enginetest.SessionSQL(t, peer, "UPDATE t SET v=v+1") // released holds/claims permit healthy peer reuse
			})
		}
	}
}

func TestMergeUpdateRepeatableReadPending(t *testing.T) {
	mergeUpdateRepeatableReadPending(t, false)
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
				mergeUpdatePendingRow(t, e, ref, original.Row, worker, holder.Transactions.Ticket, 0)
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

func TestMergeUpdateFollowChainPending(t *testing.T) {
	mergeUpdateFollowChainPending(t, false)
}

func mergeSourceFollowChainRowModes(t *testing.T) {
	mergeUpdateFollowChainPending(t, true)
}

func mergeUpdateFollowChainPending(t *testing.T, targetOnly bool) {
	for _, tc := range []struct {
		name, arms string
		strong     bool
		v          int64
	}{
		{"nonkey", "WHEN MATCHED THEN UPDATE SET v=t.v+1", false, 21},
		{"sameKey", "WHEN MATCHED THEN UPDATE SET id=t.id", true, 20},
		{"unselectedKeyArm", "WHEN MATCHED AND FALSE THEN UPDATE SET id=t.id WHEN MATCHED THEN UPDATE SET v=t.v+1", true, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			r := enginetest.SessionSQL(t, holder, "SELECT id FROM t FOR KEY SHARE")
			r.Rows.Close()
			b := enginetest.TransactionBegin(t, writer)
			enginetest.SessionSQL(t, writer, "UPDATE t SET v=20")
			q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id " + tc.arms
			if targetOnly {
				q = "MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id " + strings.ReplaceAll(tc.arms, "WHEN MATCHED", "WHEN NOT MATCHED BY SOURCE")
			}
			done := mergeUpdatePendingExecute(t, worker, q)
			mergeUpdatePendingRow(t, e, ref, row, worker, writer.Transactions.Ticket, lock.ForNoKeyUpdate)
			enginetest.TransactionEnd(t, writer, b, true)
			if tc.strong {
				mergeUpdatePendingRow(t, e, ref, row, worker, holder.Transactions.Ticket, lock.ForUpdate)
				enginetest.TransactionRows(t, peer, "SELECT v FROM t", 20)
				enginetest.TransactionEnd(t, holder, a, false)
			}
			mergeUpdatePendingOutcome(t, done, 1, nil)
			if !tc.strong {
				enginetest.TransactionEnd(t, holder, a, false)
			}
			enginetest.TransactionRows(t, peer, "SELECT v FROM t", tc.v)
			enginetest.LockEmpty(t, e)
			enginetest.SessionSQL(t, peer, "UPDATE t SET v=v+1")
		})
	}
}
