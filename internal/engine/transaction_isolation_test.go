package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestTransactionIsolationConflicts(t *testing.T) {
	for _, peer := range []string{"UPDATE t SET v=20", "DELETE FROM t", "UPDATE then DELETE"} {
		for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
			for _, finish := range []string{"COMMIT", "ROLLBACK", "cancel"} {
				t.Run(peer+"/"+own+"/"+finish, func(t *testing.T) {
					e := New()
					a := sessionStart(t, e, nil)
					b := sessionStart(t, e, nil)
					sessionSQL(t, b, "CREATE TABLE t(v INT)")
					sessionSQL(t, b, "INSERT INTO t VALUES(10)")
					sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					transactionRows(t, a, "SELECT v FROM t", 10)
					sessionSQL(t, b, "BEGIN")
					if peer == "UPDATE then DELETE" {
						sessionSQL(t, b, "UPDATE t SET v=20")
						sessionSQL(t, b, "DELETE FROM t")
					} else {
						sessionSQL(t, b, peer)
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := lockAsync(e, a, ctx, own, false)
					lockPending(t, e, 1)
					if finish == "cancel" {
						cancel()
						lockAwait(t, done, context.Canceled)
						sessionSQL(t, b, "ROLLBACK")
					} else {
						sessionSQL(t, b, finish)
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
					sessionSQL(t, a, "ROLLBACK")
					lockEmpty(t, e)
					if finish == "COMMIT" {
						if peer != "UPDATE t SET v=20" {
							transactionRows(t, b, "SELECT v FROM t")
						} else {
							transactionRows(t, b, "SELECT v FROM t", 20)
						}
					} else {
						transactionRows(t, b, "SELECT v FROM t", 10)
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
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				sessionSQL(t, b, "CREATE TABLE t(v INT)")
				sessionSQL(t, b, "INSERT INTO t VALUES(1)")
				sessionSQL(t, b, "BEGIN")
				sessionSQL(t, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				sessionSQL(t, a, "BEGIN ISOLATION LEVEL "+isolation)
				done := lockAsync(e, a, context.Background(), "SELECT v FROM t", prepare)
				lockPending(t, e, 1)
				sessionSQL(t, b, "INSERT INTO t VALUES(2)")
				sessionSQL(t, b, "COMMIT")
				lockAwait(t, done, nil)
				if isolation == "REPEATABLE READ" {
					transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1)
				} else {
					transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 2)
				}
				sessionSQL(t, a, "ROLLBACK")
				lockEmpty(t, e)
			})
		}
	}
}
func TestTransactionIsolationRootLifecycle(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1)")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	transactionRows(t, a, "SELECT v FROM t", 1)
	tOwner := a.Transactions
	tOwner.Mu.Lock()
	roots := tOwner.Roots
	frontier := tOwner.Frontier
	tOwner.Mu.Unlock()
	if len(roots) != 1 || frontier == 0 {
		t.Fatal("missing all-relation roots")
	}
	sessionSQL(t, b, "UPDATE t SET v=2")
	transactionRows(t, a, "SELECT v FROM t", 1)
	sessionSQL(t, a, "ROLLBACK")
	tOwner.Mu.Lock()
	retained := tOwner.Roots != nil || tOwner.SnapshotSet
	tOwner.Mu.Unlock()
	if retained {
		t.Fatal("retained owner roots after end")
	}
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	transactionRows(t, a, "SELECT v FROM t", 2)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := lockAsync(e, a, context.Background(), "SELECT num_nonnulls(1)", false)
	select {
	case err := <-done:
		if !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("epoch fence stalled")
	}
	sessionSQL(t, a, "ROLLBACK")
	lockEmpty(t, e)
}

func TestTransactionIsolationStatementTimeout(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1)")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	transactionRows(t, a, "SELECT v FROM t", 1)
	sessionSQL(t, b, "BEGIN")
	sessionSQL(t, b, "UPDATE t SET v=2")
	sessionSQL(t, a, "SET statement_timeout='1h'")
	ctx, cancel := context.WithCancel(context.Background())
	op, _, err := a.AcquireCommand(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	ast := statement(t, "UPDATE t SET v=3")
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
	done = lockAsync(e, a, context.Background(), "SELECT num_nonnulls(1)", false)
	select {
	case err := <-done:
		waitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
	case <-time.After(5 * time.Second):
		t.Fatal("aborted owner stalled")
	}
	sessionSQL(t, a, "ROLLBACK")
	sessionSQL(t, b, "ROLLBACK")
	transactionRows(t, b, "SELECT v FROM t", 1)
	lockEmpty(t, e)
}
