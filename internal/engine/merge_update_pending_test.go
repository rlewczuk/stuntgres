package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// These are executor/Finish and manager-state witnesses, not public SQL copies.
// A live request on the original RowID establishes the exact interleaving.
func mergeUpdatePendingRow(t *testing.T, e *Engine, ref ids.ObjectRef, row ids.RowID, worker *Session, blocker *lock.Ticket, mode lock.Mode) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: row}]
		found := false
		if entry != nil && entry.Holders[blocker].Mode != 0 {
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == worker.token && (mode == 0 || request.Mode == mode) {
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

func mergeUpdatePendingExecute(t *testing.T, s *Session, q string) <-chan deleteOutcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ast := statement(t, q)
	done := make(chan deleteOutcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		op, err := s.Acquire(ctx)
		var result Result
		if err == nil {
			result, err = s.Engine().Execute(op, ast, 0, nil)
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
				e := New()
				holder := sessionStart(t, e, nil)
				worker := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
				sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(3,30)")
				sessionSQL(t, peer, "CREATE TABLE source(id INT,x INT)")
				sessionSQL(t, peer, "INSERT INTO source VALUES(1,7)")
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				row := e.storage.Relations[ref.Address].Versions[0].Row
				tx := transactionBegin(t, holder)
				hold := "UPDATE t SET v=20 WHERE id=1"
				if lane == "keyChange" || lane == "keyChangeNoInsert" {
					hold = "UPDATE t SET id=2 WHERE id=1"
				}
				if lane == "delete" || lane == "deleteNoInsert" {
					hold = "DELETE FROM t WHERE id=1"
				}
				sessionSQL(t, holder, hold)
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
				mergeUpdatePendingRow(t, e, ref, row, worker, holder.transactions.ticket, 0)
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 30)
				if lane == "fixedSource" {
					sessionSQL(t, peer, "UPDATE source SET x=8")
				}
				if lane == "noWiden" {
					sessionSQL(t, peer, "UPDATE t SET v=10 WHERE id=3")
				}
				transactionEnd(t, holder, tx, commit)
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
					transactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 30)
				case "keyChange":
					if commit {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 99, 10, 30)
					} else {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 200, 30)
					}
				case "keyChangeNoInsert":
					if commit {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 30)
					} else {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 200, 30)
					}
				case "delete":
					v := int64(200)
					if commit {
						v = 99
					}
					transactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 30)
				case "deleteNoInsert":
					if commit {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 30)
					} else {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 200, 30)
					}
				case "fixedSource":
					transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 7, 30)
					transactionRows(t, peer, "SELECT x FROM source", 8)
				case "noWiden":
					v := int64(100)
					if commit {
						v = 20
					}
					transactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 10)
				}
				lockEmpty(t, e)
				sessionSQL(t, peer, "UPDATE t SET v=v+1") // released holds/claims permit healthy peer reuse
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
				e := New()
				holder := sessionStart(t, e, nil)
				worker := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
				sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				original := e.storage.Relations[ref.Address].Versions[0]
				sessionSQL(t, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				transactionRows(t, worker, "SELECT v FROM t WHERE id=1", 10)
				worker.transactions.mu.Lock()
				frontier := worker.transactions.frontier
				roots := make(map[ids.ObjectAddress]transactionRoot, len(worker.transactions.roots))
				for address, root := range worker.transactions.roots {
					roots[address] = root
				}
				worker.transactions.mu.Unlock()
				sessionSQL(t, worker, "UPDATE t SET v=21 WHERE id=2")
				sessionSQL(t, worker, "DELETE FROM t WHERE id=3")
				sessionSQL(t, worker, "INSERT INTO t VALUES(4,40)")
				worker.transactions.mu.Lock()
				priorWrites := append([]rowVersion(nil), worker.transactions.writes[ref.Address]...)
				priorDeletes := worker.transactions.deletes[ref.Address]
				worker.transactions.mu.Unlock()
				if targetOnly {
					sessionSQL(t, worker, "SAVEPOINT before_target_only")
				}
				tx := transactionBegin(t, holder)
				hold := "UPDATE t SET v=11 WHERE id=1"
				if deleted {
					hold = "DELETE FROM t WHERE id=1"
				}
				sessionSQL(t, holder, hold)
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
				if targetOnly {
					q = "MERGE INTO t USING (SELECT 9 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE AND t.id=1 THEN UPDATE SET v=100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,99)"
				}
				done := mergeUpdatePendingExecute(t, worker, q)
				mergeUpdatePendingRow(t, e, ref, original.Row, worker, holder.transactions.ticket, 0)
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
				transactionEnd(t, holder, tx, commit)
				if commit && deleted {
					mergeUpdatePendingOutcome(t, done, 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent delete"})
					worker.transactions.mu.Lock()
					correctOverlay := len(worker.transactions.writes) == 0 && len(worker.transactions.deletes) == 0
					if targetOnly {
						// The target-only lane has a recoverable savepoint: its
						// successful ancestor work survives the failed statement.
						correctOverlay = reflect.DeepEqual(worker.transactions.writes[ref.Address], priorWrites) && worker.transactions.deletes[ref.Address] == priorDeletes
					}
					worker.transactions.mu.Unlock()
					if !correctOverlay {
						t.Fatal("concurrent delete violated private recovery boundary")
					}
					op := transactionOperation(t, worker)
					_, err := e.Execute(op, statement(t, "SELECT * FROM t"), 0, nil)
					op.Done()
					var got *sqlstate.Error
					if !errors.As(err, &got) || got.Code != "25P02" {
						t.Fatal("missing aborted transaction", err)
					}
					if targetOnly {
						sessionSQL(t, worker, "ROLLBACK TO before_target_only")
						transactionRows(t, worker, "SELECT v FROM t ORDER BY id", 10, 21, 40)
						sessionSQL(t, worker, "RELEASE before_target_only")
					}
				} else {
					mergeUpdatePendingOutcome(t, done, 1, nil)
					worker.transactions.mu.Lock()
					sameSnapshot := worker.transactions.snapshotSet && worker.transactions.frontier == frontier && reflect.DeepEqual(worker.transactions.roots, roots)
					worker.transactions.mu.Unlock()
					if !sameSnapshot {
						t.Fatal("MERGE advanced or replaced the pinned RR snapshot")
					}
					if commit {
						transactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 10, 100, 21, 40)
					} else {
						transactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", 100, 21, 40)
					}
					// The replacement keeps logical identity and its actual predecessor base;
					// a peer descendant of snapshot10 is not a new unmatched INSERT.
					worker.transactions.mu.Lock()
					var own rowVersion
					for _, v := range worker.transactions.writes[ref.Address] {
						if v.Row == original.Row {
							own = v
						}
					}
					worker.transactions.mu.Unlock()
					if own.Row != original.Row || own.Version == original.Version || own.Base == 0 {
						t.Fatal("lost replacement identity/predecessor", own, original)
					}
					if commit && own.Base == original.Version {
						t.Fatal("replacement incorrectly based on old RR snapshot", own)
					}
					if !commit && own.Base != original.Version {
						t.Fatal("rollback failed to retain original predecessor", own)
					}
					if targetOnly {
						mergeSourceRepeatableReadFollowup(t, e, worker, commit)
					}
				}
				sessionSQL(t, worker, "ROLLBACK")
				if commit && deleted {
					transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 20, 30)
				} else {
					v := int64(10)
					if commit {
						v = 11
					}
					transactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 20, 30)
				}
				lockEmpty(t, e)
				sessionSQL(t, peer, "UPDATE t SET v=v+1")
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
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			worker := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT NOT NULL UNIQUE,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			row := e.storage.Relations[ref.Address].Versions[0].Row
			a := transactionBegin(t, holder)
			r := sessionSQL(t, holder, "SELECT id FROM t FOR KEY SHARE")
			r.Rows.Close()
			b := transactionBegin(t, writer)
			sessionSQL(t, writer, "UPDATE t SET v=20")
			q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id " + tc.arms
			if targetOnly {
				q = "MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id " + strings.ReplaceAll(tc.arms, "WHEN MATCHED", "WHEN NOT MATCHED BY SOURCE")
			}
			done := mergeUpdatePendingExecute(t, worker, q)
			mergeUpdatePendingRow(t, e, ref, row, worker, writer.transactions.ticket, lock.ForNoKeyUpdate)
			transactionEnd(t, writer, b, true)
			if tc.strong {
				mergeUpdatePendingRow(t, e, ref, row, worker, holder.transactions.ticket, lock.ForUpdate)
				transactionRows(t, peer, "SELECT v FROM t", 20)
				transactionEnd(t, holder, a, false)
			}
			mergeUpdatePendingOutcome(t, done, 1, nil)
			if !tc.strong {
				transactionEnd(t, holder, a, false)
			}
			transactionRows(t, peer, "SELECT v FROM t", tc.v)
			lockEmpty(t, e)
			sessionSQL(t, peer, "UPDATE t SET v=v+1")
		})
	}
}
