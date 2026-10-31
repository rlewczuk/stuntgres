package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestSavepointsAcceptance(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	ref := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	controlsRequire(t, a, "SAVEPOINT s")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	controlsRequire(t, a, "ROLLBACK TO s")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(3)")
	enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 3)
	enginetest.TransactionRows(t, b, "SELECT v FROM t")
	if !enginetest.TransactionEnd(t, a, ref, true).Committed {
		t.Fatal("Go reference lost through recovery")
	}
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 3)
}

func TestSavepointsWriteState(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	controlsRequire(t, a, "BEGIN")
	for _, q := range []string{"INSERT INTO t VALUES(1)", "INSERT INTO t VALUES(2)", "INSERT INTO t VALUES(3)"} {
		enginetest.SessionSQL(t, a, q)
	}
	controlsRequire(t, a, "SAVEPOINT s")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(4)")
	retained := enginetest.SessionSQL(t, a, "SELECT v FROM t ORDER BY v")
	defer retained.Rows.Close()
	controlsRequire(t, a, "ROLLBACK TO s")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(9)")
	if got := enginetest.Tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}) {
		t.Fatal("abandoned suffix overwritten", got)
	}
	enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 2, 3, 9)
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(7)")
	controlsRequire(t, a, "ROLLBACK TO s")
	enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 2, 3, 7)
	controlsRequire(t, a, "COMMIT")
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2, 3, 7)
}

func TestSavepointsLifecycle(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "physical"} {
		t.Run(end, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			controlsRequire(t, a, "BEGIN")
			controlsRequire(t, a, "SAVEPOINT s")
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ticket := a.Transactions.Ticket
			controlsRequire(t, a, "ROLLBACK TO s")
			if a.Transactions.Ticket != ticket || len(e.Locks.Inspect(ticket).Journals[ticket]) != 0 {
				t.Fatal("rollback did not release postmark contributions")
			}
			controlsAbort(t, a)
			if a.Transactions.Ticket != ticket || len(e.Locks.Inspect(ticket).Journals[ticket]) != 0 {
				t.Fatal("recoverable failure lost ticket or retained child contributions")
			}
			controlsRequire(t, a, "ROLLBACK TO s")
			if end == "physical" {
				if err := core.EndTransactionSession(a.Transactions, a.SessionToken); err != nil {
					t.Fatal(err)
				}
			} else {
				controlsRequire(t, a, end)
			}
			if len(a.Transactions.Savepoints) != 0 {
				t.Fatal("marks leaked at end")
			}
			if strings.Contains(end, "CHAIN") {
				controlsRequire(t, a, "ROLLBACK")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestSavepointsAuthorityAndCompletion(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel_before", true: "cancel_after"}[late], func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			controlsRequire(t, a, "BEGIN")
			ref, command, frontier := a.Transactions.Ref, a.Transactions.Command, e.Visibility.CommitSeq()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := a.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			if !late {
				cancel()
			}
			_, err = scope.Execute(enginetest.Statement(t, "SAVEPOINT s"), 0, nil)
			if late {
				// Cancellation after successful execution must not retract an
				// accepted control when the caller completes the statement.
				cancel()
			}
			if late {
				if err != nil || scope.Finish(nil) != nil || scope.Finish(errors.New("late")) != nil || len(a.Transactions.Savepoints) != 1 {
					t.Fatal("accepted control retracted", err)
				}
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				scope.Finish(err)
				if len(a.Transactions.Savepoints) != 0 {
					t.Fatal("canceled mark installed")
				}
			}
			if a.Transactions.Ref != ref || a.Transactions.Command != command || e.Visibility.CommitSeq() != frontier {
				t.Fatal("control advanced transaction identity/frontier")
			}
		})
	}
}

func TestSavepointsCancellationAndControls(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	controlsRequire(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	controlsRequire(t, a, "SAVEPOINT s")
	var dump strings.Builder
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "VALUES") {
		t.Fatal("Dump published private effects", dump.String())
	}
	if err := engine.Restore(context.Background(), e, strings.NewReader("SAVEPOINT s;")); err == nil {
		t.Fatal("Restore accepted control")
	}
	controlsRequire(t, a, "ROLLBACK TO s")
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := controlsExecute(t, a, "ROLLBACK TO s"); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("old mark resurrected", err)
	}
	controlsRequire(t, a, "ROLLBACK")
	controlsRequire(t, a, "BEGIN")
	if len(a.Transactions.Savepoints) != 0 {
		t.Fatal("old mark survived fresh block")
	}
	controlsRequire(t, a, "ROLLBACK")
}

func TestSavepointsOperationAuthority(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, engine.New(), nil)
	controlsRequire(t, a, "BEGIN")
	controlsRequire(t, a, "SAVEPOINT s")
	op := enginetest.TransactionOperation(t, a)
	copyOp := *op
	missing := *op
	missing.Self = &missing
	done := enginetest.TransactionOperation(t, a)
	done.Done()
	foreign := enginetest.TransactionOperation(t, b)
	for _, candidate := range []*session.Operation{nil, &copyOp, &missing, done, foreign} {
		if _, err := e.Execute(candidate, enginetest.Statement(t, "ROLLBACK TO s"), 0, nil); err == nil {
			t.Fatal("invalid operation admitted")
		}
		if a.TransactionState() != session.TxActive || len(a.Transactions.Savepoints) != 1 {
			t.Fatal("invalid operation mutated mark authority")
		}
	}
	op.Done()
	a.Transactions.Command = ids.CommandID(^uint64(0))
	a.Transactions.Next = ids.TxID(^uint64(0))
	e.Visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
	for _, q := range []string{"ROLLBACK TO s", "RELEASE s", "SAVEPOINT s"} {
		controlsRequire(t, a, q)
	}
	if a.Transactions.Command != ids.CommandID(^uint64(0)) || a.Transactions.Next != ids.TxID(^uint64(0)) || e.Visibility.CommitSeq() != ids.CommitSeq(^uint64(0)) {
		t.Fatal("savepoint control advanced exhausted counters")
	}
	controlsRequire(t, a, "ROLLBACK")
}

func TestSavepointsErrorRecovery(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	controlsRequire(t, a, "BEGIN")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	controlsRequire(t, a, "SAVEPOINT s")
	ref, ticket := a.Transactions.Ref, a.Transactions.Ticket
	controlsRequire(t, a, "SAVEPOINT child")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	controlsRequire(t, a, "RELEASE child")
	controlsAbort(t, a)
	if a.TransactionState() != session.TxAborted || a.Transactions.Ref != ref || a.Transactions.Ticket != ticket || len(a.Transactions.Savepoints) != 1 {
		t.Fatal("recoverable failure lost ancestor authority")
	}
	if err := controlsDescribe(t, a, "ROLLBACK TO s"); err != nil || a.TransactionState() != session.TxAborted {
		t.Fatal("Describe recovered or rejected rollback", err)
	}
	for range 2 {
		if err := controlsExecute(t, a, "ROLLBACK TO missing"); err == nil {
			t.Fatal("unknown mark accepted")
		}
		if a.TransactionState() != session.TxAborted || len(a.Transactions.Savepoints) != 1 {
			t.Fatal("failed recovery destroyed ancestor")
		}
	}
	controlsRequire(t, a, "ROLLBACK TO s")
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
	controlsRequire(t, a, "ROLLBACK TO s")
	enginetest.TransactionRows(t, a, "SELECT v FROM t", 1)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(3)")
	if !enginetest.TransactionEnd(t, a, ref, true).Committed {
		t.Fatal("Go ref did not survive rollback-to")
	}
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 3)
}
