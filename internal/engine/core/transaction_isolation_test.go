package core_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

func TestTransactionIsolationConflicts(t *testing.T) {
	for _, peer := range []string{"UPDATE t SET v=20", "DELETE FROM t", "UPDATE then DELETE"} {
		for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
			for _, finish := range []string{"COMMIT", "ROLLBACK", "cancel"} {
				t.Run(peer+"/"+own+"/"+finish, func(t *testing.T) {
					e := engine.New()
					a := enginetest.StartSession(t, e, nil)
					b := enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
					enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(10)")
					enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					enginetest.TransactionRows(t, a, "SELECT v FROM t", 10)
					enginetest.SessionSQL(t, b, "BEGIN")
					if peer == "UPDATE then DELETE" {
						enginetest.SessionSQL(t, b, "UPDATE t SET v=20")
						enginetest.SessionSQL(t, b, "DELETE FROM t")
					} else {
						enginetest.SessionSQL(t, b, peer)
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := enginetest.LockAsync(e, a, ctx, own, false)
					enginetest.LockPending(t, e, 1)
					if finish == "cancel" {
						cancel()
						enginetest.LockAwait(t, done, context.Canceled)
						enginetest.SessionSQL(t, b, "ROLLBACK")
					} else {
						enginetest.SessionSQL(t, b, finish)
						select {
						case err := <-done:
							if finish == "ROLLBACK" {
								if err != nil {
									t.Fatal(err)
								}
							} else {
								word := "update"
								if peer == "DELETE FROM t" {
									word = "delete"
								}
								waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent "+word)
							}
						case <-time.After(5 * time.Second):
							t.Fatal("row wait did not finish")
						}
					}
					enginetest.SessionSQL(t, a, "ROLLBACK")
					enginetest.LockEmpty(t, e)
					if finish == "COMMIT" {
						if peer != "UPDATE t SET v=20" {
							enginetest.TransactionRows(t, b, "SELECT v FROM t")
						} else {
							enginetest.TransactionRows(t, b, "SELECT v FROM t", 20)
						}
					} else {
						enginetest.TransactionRows(t, b, "SELECT v FROM t", 10)
					}
				})
			}
		}
	}
}
func TestTransactionIsolationSnapshotPhases(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "READ UNCOMMITTED", "REPEATABLE READ"} {
		for _, prepare := range []bool{false, true} {
			t.Run(isolation+map[bool]string{false: "/execute", true: "/describe"}[prepare], func(t *testing.T) {
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				b := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
				enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1)")
				enginetest.SessionSQL(t, b, "BEGIN")
				enginetest.SessionSQL(t, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL "+isolation)
				done := enginetest.LockAsync(e, a, context.Background(), "SELECT v FROM t", prepare)
				enginetest.LockPending(t, e, 1)
				enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(2)")
				enginetest.SessionSQL(t, b, "COMMIT")
				enginetest.LockAwait(t, done, nil)
				if isolation == "REPEATABLE READ" {
					enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1)
				} else {
					enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 2)
				}
				enginetest.SessionSQL(t, a, "ROLLBACK")
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
func TestTransactionIsolationRootLifecycle(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
	tOwner := a.Transactions
	tOwner.Mu.Lock()
	roots := tOwner.Roots
	frontier := tOwner.Frontier
	tOwner.Mu.Unlock()
	if len(roots) != 1 || frontier == 0 {
		t.Fatal("missing all-relation roots")
	}
	enginetest.SessionSQL(t, b, "UPDATE t SET v=2")
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
	enginetest.SessionSQL(t, a, "ROLLBACK")
	tOwner.Mu.Lock()
	retained := tOwner.Roots != nil || tOwner.SnapshotSet
	tOwner.Mu.Unlock()
	if retained {
		t.Fatal("retained owner roots after end")
	}
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 2)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	done := enginetest.LockAsync(e, a, context.Background(), "SELECT num_nonnulls(1)", false)
	select {
	case err := <-done:
		if !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("epoch fence stalled")
	}
	enginetest.SessionSQL(t, a, "ROLLBACK")
	enginetest.LockEmpty(t, e)
}

func TestTransactionIsolationStatementTimeout(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
	enginetest.SessionSQL(t, b, "BEGIN")
	enginetest.SessionSQL(t, b, "UPDATE t SET v=2")
	enginetest.SessionSQL(t, a, "SET statement_timeout='1h'")
	ctx, cancel := context.WithCancel(context.Background())
	op, _, err := a.AcquireCommand(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	ast := enginetest.Statement(t, "UPDATE t SET v=3")
	worker := make(chan error, 1)
	var done <-chan error = worker
	go func() {
		result, err := e.Execute(op, ast, 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
		}
		err = op.CommandError(err)
		op.Done()
		worker <- err
		close(worker)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-worker:
		case <-time.After(5 * time.Second):
			t.Error("statement timeout worker did not join")
		}
	})
	request := timeoutPendingRequest(t, e, op)
	rowLockQueued(t, e, b.Transactions.Ticket, lock.ForNoKeyUpdate)
	timeoutPendingPublish(t, e, request, op.StatementDeadline)
	select {
	case err := <-done:
		waitsDiagnostic(t, err, "57014", "canceling statement due to statement timeout")
	case <-time.After(5 * time.Second):
		t.Fatal("statement timer did not end row wait")
	}
	done = enginetest.LockAsync(e, a, context.Background(), "SELECT num_nonnulls(1)", false)
	select {
	case err := <-done:
		waitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
	case <-time.After(5 * time.Second):
		t.Fatal("aborted owner stalled")
	}
	enginetest.SessionSQL(t, a, "ROLLBACK")
	enginetest.SessionSQL(t, b, "ROLLBACK")
	enginetest.TransactionRows(t, b, "SELECT v FROM t", 1)
	enginetest.LockEmpty(t, e)
}

func timeoutPendingRequest(t *testing.T, e *engine.Engine, op *session.Operation) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		var found *lock.Request
		for _, entry := range lockState.Resources {
			for _, request := range entry.Pending {
				if request.Policy.TimeoutSlot == &op.LockWait && !request.Terminal {
					found = request.Request
				}
			}
		}
		if found != nil {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatal("exact operation did not queue behind holder")
		}
		runtime.Gosched()
	}
}

func timeoutPendingPublish(t *testing.T, e *engine.Engine, request *lock.Request, now time.Time) {
	t.Helper()
	// The real cancellation callback may already own terminal publication
	// after a failing lifecycle assertion. Only the manager's first terminal
	// transition owns notification; never overwrite or renotify its result.
	if err := e.Locks.ExpireWait(request, now); err == nil && !request.Terminal() {
		t.Fatal("explicit due instant did not produce queued ERROR")
	}
}
