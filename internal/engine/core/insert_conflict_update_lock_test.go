package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type conflictUpdateWorkerResult struct {
	result core.Result
	err    error
}
type conflictUpdateWorker struct {
	done   chan conflictUpdateWorkerResult
	cancel context.CancelFunc
	joined bool
}

func conflictUpdateStartWorker(t *testing.T, e *core.Engine, s *session.Session, q string) *conflictUpdateWorker {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	worker := &conflictUpdateWorker{done: make(chan conflictUpdateWorkerResult, 1), cancel: cancel}
	go func() {
		op, err := s.Acquire(ctx)
		var result core.Result
		if err == nil {
			result, err = e.Execute(op, mustLockStatement(q), 0, nil)
			op.Done()
		}
		worker.done <- conflictUpdateWorkerResult{result, err}
	}()
	t.Cleanup(func() {
		cancel()
		if !worker.joined {
			select {
			case outcome := <-worker.done:
				if outcome.result.Rows != nil {
					outcome.result.Rows.Close()
				}
				worker.joined = true
			case <-time.After(5 * time.Second):
				t.Error("conflict worker did not join")
			}
		}
	})
	return worker
}
func (w *conflictUpdateWorker) await(t *testing.T) conflictUpdateWorkerResult {
	t.Helper()
	select {
	case result := <-w.done:
		w.joined = true
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("conflict worker did not terminate")
		return conflictUpdateWorkerResult{}
	}
}
func conflictUpdatePendingRow(t *testing.T, e *core.Engine, ref ids.ObjectRef, id ids.RowID) (*lock.Ticket, lock.Mode) {
	t.Helper()
	enginetest.LockPending(t, e, 1)
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: id}]
	if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Resource.Key() != nil {
		t.Fatal("not the exact selected logical-row pending barrier", ref, id)
	}
	return entry.Pending[0].Ticket, entry.Pending[0].Mode
}
func conflictUpdateWantError(t *testing.T, outcome conflictUpdateWorkerResult, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(outcome.err, &d) || d.Code != code || d.Message != message || outcome.result.Count != 0 || outcome.result.Rows != nil {
		t.Fatal("failed command/result", outcome, code, message)
	}
}

func TestInsertConflictUpdateRowRecheck(t *testing.T) {
	t.Run("invisible_source", func(t *testing.T) {
		e := engine.New()
		holder := enginetest.StartSession(t, e, nil)
		worker := enginetest.StartSession(t, e, nil)
		observer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, observer, "CREATE TABLE t(k INT UNIQUE,v INT)")
		enginetest.SessionSQL(t, observer, "CREATE TABLE src(k INT,v INT)")
		enginetest.SessionSQL(t, observer, "INSERT INTO src VALUES(1,1)")
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
		source := enginetest.CatalogRef(t, e, e.CatalogRead(), "src")
		holderTx := enginetest.TransactionBegin(t, holder)
		enginetest.SessionSQL(t, holder, "INSERT INTO t VALUES(1,10)")
		frontier := e.Visibility.CommitSeq()
		running := conflictUpdateStartWorker(t, e, worker, "INSERT INTO t SELECT src.k,src.v FROM src LEFT JOIN t ON t.k=src.k WHERE t.k IS NULL ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v")
		enginetest.LockPending(t, e, 1)
		request := func() lock.Waiter {
			lockState := e.Locks.Inspect()
			for _, entry := range lockState.Resources {
				if len(entry.Pending) == 0 {
					continue
				}
				pending := entry.Pending[0]
				if len(entry.Pending) != 1 || pending.Mode != lock.UniqueClaim || pending.Resource.Key() == nil ||
					pending.Policy.TimeoutSlot == nil ||
					len(pending.Resource.Key().Value().([]sqltype.Value)) != 1 || enginetest.Export(pending.Resource.Key().Value().([]sqltype.Value)[0]) != int64(1) ||
					len(entry.Holders) != 1 {
					t.Fatal("not the actual source proposal's UniqueKey queue", pending)
				}
				for ticket, hold := range entry.Holders {
					if ticket.Ref() != holderTx || hold.Mode != lock.UniqueClaim {
						t.Fatal("wrong uncommitted insertion blocker", ticket.Ref(), hold)
					}
				}
				return pending
			}
			t.Fatal("missing source proposal key request")
			return lock.Waiter{}
		}()
		// The request's timeout slot identifies the worker's admitted operation.
		worker.Mu.Lock()
		var scope *core.StatementScope
		for operation := range worker.Operations {
			if &operation.LockWait == request.Policy.TimeoutSlot {
				scope = core.ScopeOfLease(operation.Lease)
			}
		}
		worker.Mu.Unlock()
		if scope == nil || scope.LockedRef != ref || scope.View == nil ||
			scope.InsertConflict == nil || len(scope.InsertConflict.Selected) != 1 ||
			request.Resource.Address != scope.InsertConflict.Selected[0] || request.Resource.Epoch != ref.Epoch {
			t.Fatal("wrong admitted source/conflict capture", scope, request.Resource)
		}
		captured := scope.View.Load()
		if captured == nil {
			t.Fatal("missing source visibility view")
		}
		_, targetCaptured := captured.Committed[ref.Address]
		if !targetCaptured || captured.Frontier != frontier || len(captured.Committed[ref.Address]) != 0 ||
			len(captured.Own[ref.Address]) != 0 || len(captured.Committed[source.Address]) != 1 ||
			enginetest.Export(captured.Committed[source.Address][0].Values[1]) != int64(1) {
			t.Fatal("source snapshot sees the uncommitted conflict identity", captured)
		}
		enginetest.TransactionEnd(t, holder, holderTx, true)
		outcome := running.await(t)
		if outcome.err != nil || outcome.result.Rows != nil || outcome.result.Count != 1 {
			t.Fatal("exact invisible-source action count", outcome)
		}
		if captured.Frontier != frontier || len(captured.Committed[ref.Address]) != 0 {
			t.Fatal("source snapshot was replaced during conflict admission")
		}
		enginetest.TransactionRows(t, observer, "SELECT k FROM t", 1)
		enginetest.TransactionRows(t, observer, "SELECT v FROM t", 11)
		enginetest.LockEmpty(t, e)
	})
	for _, level := range []string{"READ COMMITTED", "READ UNCOMMITTED"} {
		for _, commit := range []bool{false, true} {
			for _, condition := range []string{"TRUE", "t.v<15", "t.v>15"} {
				t.Run(fmt.Sprintf("latest_payload/%s/commit_%t/%s", level, commit, condition), func(t *testing.T) {
					e := engine.New()
					holder := enginetest.StartSession(t, e, nil)
					worker := enginetest.StartSession(t, e, nil)
					observer := enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, observer, "CREATE TABLE t(k INT UNIQUE,v INT)")
					enginetest.SessionSQL(t, observer, "INSERT INTO t VALUES(1,10)")
					ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
					original := e.Storage.Relations[ref.Address].Versions[0]
					workerTx := enginetest.TransactionBegin(t, worker)
					enginetest.SessionSQL(t, worker, "SET TRANSACTION ISOLATION LEVEL "+level)
					enginetest.TransactionRows(t, worker, "SELECT v FROM t", 10)
					holderTx := enginetest.TransactionBegin(t, holder)
					enginetest.TransactionRows(t, holder, "SELECT v FROM t FOR UPDATE", 10)
					running := conflictUpdateStartWorker(t, e, worker, "INSERT INTO t VALUES(2,30),(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v WHERE "+condition)
					ticket, mode := conflictUpdatePendingRow(t, e, ref, original.Row)
					if mode != lock.ForNoKeyUpdate || ticket.Ref() != workerTx {
						t.Fatal("not the worker's exact selected-row NKU barrier", ticket.Ref(), mode)
					}
					enginetest.SessionSQL(t, holder, "UPDATE t SET v=20 WHERE k=1")
					enginetest.TransactionEnd(t, holder, holderTx, commit)
					before := int64(10)
					if commit {
						before = 20
					}
					matches := condition == "TRUE" || condition == "t.v<15" && before < 15 || condition == "t.v>15" && before > 15
					wantCount := int64(1)
					wantValue := before
					if matches {
						wantCount++
						wantValue++
					}
					outcome := running.await(t)
					if outcome.err != nil || outcome.result.Rows != nil || outcome.result.Count != wantCount {
						t.Fatal("latest payload/predicate plus mixed prefix", outcome, wantCount)
					}
					enginetest.TransactionRows(t, worker, "SELECT k FROM t ORDER BY k", 1, 2)
					enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY k", wantValue, 30)
					enginetest.TransactionEnd(t, worker, workerTx, true)
					enginetest.TransactionRows(t, observer, "SELECT k FROM t ORDER BY k", 1, 2)
					enginetest.TransactionRows(t, observer, "SELECT v FROM t ORDER BY k", wantValue, 30)
					if enginetest.Export(original.Values[1]) != int64(10) {
						t.Fatal("captured predecessor mutated")
					}
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
	for _, level := range []string{"READ COMMITTED", "READ UNCOMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
		for _, condition := range []string{"FALSE", "NULL"} {
			for _, finish := range []string{"update", "delete", "move", "rollback", "cancel", "reset"} {
				t.Run(level+condition+finish, func(t *testing.T) {
					e := engine.New()
					holder := enginetest.StartSession(t, e, nil)
					worker := enginetest.StartSession(t, e, nil)
					observer := enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, observer, "CREATE TABLE t(k INT UNIQUE,v INT)")
					enginetest.SessionSQL(t, observer, "INSERT INTO t VALUES(1,10)")
					ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
					original := e.Storage.Relations[ref.Address].Versions[0]
					workerTx := enginetest.TransactionBegin(t, worker)
					enginetest.SessionSQL(t, worker, "SET TRANSACTION ISOLATION LEVEL "+level)
					enginetest.TransactionRows(t, worker, "SELECT v FROM t", 10)
					holderTx := enginetest.TransactionBegin(t, holder)
					enginetest.TransactionRows(t, holder, "SELECT v FROM t FOR UPDATE", 10)
					running := conflictUpdateStartWorker(t, e, worker, "INSERT INTO t VALUES(2,20),(1,1) ON CONFLICT(k) DO UPDATE SET v=excluded.v WHERE "+condition)
					ticket, mode := conflictUpdatePendingRow(t, e, ref, original.Row)
					if mode != lock.ForNoKeyUpdate {
						t.Fatal("payload SET initially requests NKU", mode)
					}
					mark := e.Locks.CheckpointUnique(ticket, nil)
					if len(mark) != 1 {
						t.Fatal("new candidate claim not released/prior staged claim lost", mark)
					}
					switch finish {
					case "update":
						enginetest.SessionSQL(t, holder, "UPDATE t SET v=11 WHERE k=1")
						enginetest.TransactionEnd(t, holder, holderTx, true)
					case "delete":
						enginetest.SessionSQL(t, holder, "DELETE FROM t WHERE k=1")
						enginetest.TransactionEnd(t, holder, holderTx, true)
					case "move":
						enginetest.SessionSQL(t, holder, "UPDATE t SET k=3 WHERE k=1")
						enginetest.TransactionEnd(t, holder, holderTx, true)
					case "rollback":
						enginetest.SessionSQL(t, holder, "UPDATE t SET v=11 WHERE k=1")
						enginetest.TransactionEnd(t, holder, holderTx, false)
					case "cancel":
						running.cancel()
						enginetest.TransactionEnd(t, holder, holderTx, false)
					case "reset":
						if err := engine.Reset(context.Background(), e); err != nil {
							t.Fatal(err)
						}
						enginetest.TransactionEnd(t, holder, holderTx, false)
					}
					outcome := running.await(t)
					stable := level == "REPEATABLE READ" || level == "SERIALIZABLE"
					switch {
					case finish == "cancel":
						if !errors.Is(outcome.err, context.Canceled) || outcome.result.Count != 0 || outcome.result.Rows != nil {
							t.Fatal(outcome)
						}
					case finish == "reset":
						if !errors.Is(outcome.err, ids.ErrCatalogReplaced) || outcome.result.Count != 0 || outcome.result.Rows != nil {
							t.Fatal(outcome)
						}
					case stable && finish != "rollback":
						message := "could not serialize access due to concurrent update"
						if finish == "delete" {
							message = "could not serialize access due to concurrent delete"
						}
						conflictUpdateWantError(t, outcome, "40001", message)
					default:
						want := int64(1)
						if finish == "delete" || finish == "move" {
							want = 2
						}
						if outcome.err != nil || outcome.result.Rows != nil || outcome.result.Count != want {
							t.Fatal("fresh head/restart/filter count", outcome, want)
						}
					}
					enginetest.TransactionEnd(t, worker, workerTx, false)
					if finish == "reset" {
						enginetest.LockEmpty(t, e)
						return
					}
					switch finish {
					case "delete":
						enginetest.TransactionRows(t, observer, "SELECT v FROM t")
					case "update":
						enginetest.TransactionRows(t, observer, "SELECT v FROM t", 11)
					default:
						enginetest.TransactionRows(t, observer, "SELECT v FROM t", 10)
					}
					if enginetest.Export(original.Values[1]) != int64(10) {
						t.Fatal("captured predecessor mutated")
					}
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
}

func TestInsertConflictUpdateLockOrdering(t *testing.T) {
	for _, set := range []string{"v=excluded.v", "k=t.k"} {
		t.Run(set, func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			observer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, observer, "CREATE TABLE t(k INT UNIQUE,v INT)")
			enginetest.SessionSQL(t, observer, "INSERT INTO t VALUES(1,10)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			id := e.Storage.Relations[ref.Address].Versions[0].Row
			workerTx := enginetest.TransactionBegin(t, worker)
			enginetest.SessionSQL(t, worker, "INSERT INTO t VALUES(4,40)")
			baseline := e.Locks.CheckpointUnique(worker.Transactions.Ticket, nil)
			holderTx := enginetest.TransactionBegin(t, holder)
			enginetest.TransactionRows(t, holder, "SELECT v FROM t FOR UPDATE", 10)
			running := conflictUpdateStartWorker(t, e, worker, "INSERT INTO t VALUES(2,20),(1,1) ON CONFLICT(k) DO UPDATE SET "+set+" WHERE FALSE")
			ticket, mode := conflictUpdatePendingRow(t, e, ref, id)
			wantMode := lock.ForNoKeyUpdate
			if set == "k=t.k" {
				wantMode = lock.ForUpdate
			}
			if mode != wantMode {
				t.Fatal("SET-slot initial row class", mode, wantMode)
			}
			before := e.Locks.CheckpointUnique(ticket, nil)
			if len(before) != 2 {
				t.Fatal("row waiter must retain old key4 and earlier command-prefix key2 only", before)
			}
			for resource, mode := range baseline {
				if before[resource].Mode != mode.Mode || len(before[resource].Multi) != 0 || len(mode.Multi) != 0 {
					t.Fatal("prior transaction grant lost")
				}
			}
			enginetest.TransactionEnd(t, holder, holderTx, true)
			outcome := running.await(t)
			if outcome.err != nil || outcome.result.Count != 1 {
				t.Fatal(outcome)
			}
			after := e.Locks.CheckpointUnique(ticket, nil)
			if len(after) != len(before) {
				t.Fatal("FALSE branch retained speculative key", before, after)
			}
			for resource, mode := range before {
				if after[resource].Mode != mode.Mode || len(after[resource].Multi) != 0 || len(mode.Multi) != 0 {
					t.Fatal("FALSE branch changed retained claim", resource)
				}
			}
			if e.Locks.Checkpoint(ticket)[lock.Resource{ObjectRef: ref, Row: id}].Mode&wantMode == 0 {
				t.Fatal("FALSE branch lost row grant")
			}
			// Omitting the same key is independent of the retained row lock.
			omitted := enginetest.SessionSQL(t, observer, "INSERT INTO t VALUES(1,99) ON CONFLICT(k) DO NOTHING")
			if omitted.Count != 0 {
				t.Fatal(omitted)
			}
			updating := conflictUpdateStartWorker(t, e, observer, "UPDATE t SET v=12 WHERE k=1")
			conflictUpdatePendingRow(t, e, ref, id)
			enginetest.TransactionEnd(t, worker, workerTx, true)
			updated := updating.await(t)
			if updated.err != nil || updated.result.Count != 1 {
				t.Fatal(updated)
			}
			enginetest.TransactionRows(t, observer, "SELECT v FROM t ORDER BY k", 12, 20, 40)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestInsertConflictUpdateAuthorizationAfterWait(t *testing.T) {
	for _, privilege := range []string{"UPDATE", "SELECT", "INSERT"} {
		t.Run(privilege, func(t *testing.T) {
			e := engine.New()
			admin := enginetest.StartSession(t, e, nil)
			holder := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, admin, "CREATE ROLE upsert_waiter")
			enginetest.SessionSQL(t, admin, "CREATE TABLE t(k INT UNIQUE,v INT)")
			enginetest.SessionSQL(t, admin, "INSERT INTO t VALUES(1,10),(3,30)")
			enginetest.SessionSQL(t, admin, "GRANT INSERT,UPDATE,SELECT ON t TO upsert_waiter")
			enginetest.SessionSQL(t, worker, "SET ROLE upsert_waiter")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			id := e.Storage.Relations[ref.Address].Versions[1].Row
			holderTx := enginetest.TransactionBegin(t, holder)
			enginetest.TransactionRows(t, holder, "SELECT v FROM t WHERE k=3 FOR UPDATE", 30)
			running := conflictUpdateStartWorker(t, e, worker, "INSERT INTO t VALUES(2,20),(1,11),(3,31) ON CONFLICT(k) DO UPDATE SET v=excluded.v")
			conflictUpdatePendingRow(t, e, ref, id)
			enginetest.SessionSQL(t, admin, fmt.Sprintf("REVOKE %s ON t FROM upsert_waiter", privilege))
			enginetest.TransactionEnd(t, holder, holderTx, true)
			outcome := running.await(t)
			conflictUpdateWantError(t, outcome, "42501", "permission denied for table t")
			enginetest.TransactionRows(t, admin, "SELECT v FROM t ORDER BY k", 10, 30)
			enginetest.LockEmpty(t, e)
		})
	}
}
