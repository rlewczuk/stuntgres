package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestSavepointsAcceptance(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	ref := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	controlsRequire(t, a, "SAVEPOINT s")
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	controlsRequire(t, a, "ROLLBACK TO s")
	sessionSQL(t, a, "INSERT INTO t VALUES(3)")
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 3)
	transactionRows(t, b, "SELECT v FROM t")
	if !transactionEnd(t, a, ref, true).Committed {
		t.Fatal("Go reference lost through recovery")
	}
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 3)
}

func TestSavepointsWriteState(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	controlsRequire(t, a, "BEGIN")
	for _, q := range []string{"INSERT INTO t VALUES(1)", "INSERT INTO t VALUES(2)", "INSERT INTO t VALUES(3)"} {
		sessionSQL(t, a, q)
	}
	controlsRequire(t, a, "SAVEPOINT s")
	sessionSQL(t, a, "INSERT INTO t VALUES(4)")
	retained := sessionSQL(t, a, "SELECT v FROM t ORDER BY v")
	defer retained.Rows.Close()
	controlsRequire(t, a, "ROLLBACK TO s")
	sessionSQL(t, a, "INSERT INTO t VALUES(9)")
	if got := tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}) {
		t.Fatal("abandoned suffix overwritten", got)
	}
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 2, 3, 9)
	sessionSQL(t, b, "INSERT INTO t VALUES(7)")
	controlsRequire(t, a, "ROLLBACK TO s")
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 2, 3, 7)
	controlsRequire(t, a, "COMMIT")
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2, 3, 7)
}

func TestSavepointsLifecycle(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "physical"} {
		t.Run(end, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			controlsRequire(t, a, "BEGIN")
			controlsRequire(t, a, "SAVEPOINT s")
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ticket := a.Transactions.ticket
			controlsRequire(t, a, "ROLLBACK TO s")
			if a.Transactions.ticket != ticket || len(e.locks.Inspect(ticket).Journals[ticket]) != 0 {
				t.Fatal("rollback did not release postmark contributions")
			}
			controlsAbort(t, a)
			if a.Transactions.ticket != ticket || len(e.locks.Inspect(ticket).Journals[ticket]) != 0 {
				t.Fatal("recoverable failure lost ticket or retained child contributions")
			}
			controlsRequire(t, a, "ROLLBACK TO s")
			if end == "physical" {
				if err := endTransactionSession(a.Transactions, a.SessionToken); err != nil {
					t.Fatal(err)
				}
			} else {
				controlsRequire(t, a, end)
			}
			if len(a.Transactions.savepoints) != 0 {
				t.Fatal("marks leaked at end")
			}
			if strings.Contains(end, "CHAIN") {
				controlsRequire(t, a, "ROLLBACK")
			}
			lockEmpty(t, e)
		})
	}
}

func TestSavepointsAuthorityAndCompletion(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel_before", true: "cancel_after"}[late], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			controlsRequire(t, a, "BEGIN")
			ref, command, frontier := a.Transactions.ref, a.Transactions.command, e.visibility.CommitSeq()
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
			// Classification and gate each check Err; the third check is final
			// installation under publication synchronization.
			barrier := &transactionBarrierContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
			if late {
				barrier.late = cancel
			}
			op.Ctx = barrier
			done := make(chan error, 1)
			go func() { _, err := scope.Execute(statement(t, "SAVEPOINT s"), 0, nil); done <- err }()
			<-barrier.entered
			if !late {
				cancel()
			}
			close(barrier.release)
			err = <-done
			if late {
				if err != nil || scope.Finish(nil) != nil || scope.Finish(errors.New("late")) != nil || len(a.Transactions.savepoints) != 1 {
					t.Fatal("accepted control retracted", err)
				}
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				scope.Finish(err)
				if len(a.Transactions.savepoints) != 0 {
					t.Fatal("canceled mark installed")
				}
			}
			if a.Transactions.ref != ref || a.Transactions.command != command || e.visibility.CommitSeq() != frontier {
				t.Fatal("control advanced transaction identity/frontier")
			}
		})
	}
}

func TestSavepointsCancellationAndControls(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	controlsRequire(t, a, "SAVEPOINT s")
	var dump strings.Builder
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "VALUES") {
		t.Fatal("Dump published private effects", dump.String())
	}
	if err := e.Restore(context.Background(), strings.NewReader("SAVEPOINT s;")); err == nil {
		t.Fatal("Restore accepted control")
	}
	controlsRequire(t, a, "ROLLBACK TO s")
	transactionRows(t, a, "SELECT v FROM t", 1)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := controlsExecute(t, a, "ROLLBACK TO s"); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("old mark resurrected", err)
	}
	controlsRequire(t, a, "ROLLBACK")
	controlsRequire(t, a, "BEGIN")
	if len(a.Transactions.savepoints) != 0 {
		t.Fatal("old mark survived fresh block")
	}
	controlsRequire(t, a, "ROLLBACK")
}

func TestSavepointsOperationAuthority(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, New(), nil)
	controlsRequire(t, a, "BEGIN")
	controlsRequire(t, a, "SAVEPOINT s")
	op := transactionOperation(t, a)
	copyOp := *op
	missing := *op
	missing.Self = &missing
	done := transactionOperation(t, a)
	done.Done()
	foreign := transactionOperation(t, b)
	for _, candidate := range []*Operation{nil, &copyOp, &missing, done, foreign} {
		if _, err := e.Execute(candidate, statement(t, "ROLLBACK TO s"), 0, nil); err == nil {
			t.Fatal("invalid operation admitted")
		}
		if a.TransactionState() != TxActive || len(a.Transactions.savepoints) != 1 {
			t.Fatal("invalid operation mutated mark authority")
		}
	}
	op.Done()
	a.Transactions.command = ids.CommandID(^uint64(0))
	a.Transactions.next = ids.TxID(^uint64(0))
	e.visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
	for _, q := range []string{"ROLLBACK TO s", "RELEASE s", "SAVEPOINT s"} {
		controlsRequire(t, a, q)
	}
	if a.Transactions.command != ids.CommandID(^uint64(0)) || a.Transactions.next != ids.TxID(^uint64(0)) || e.visibility.CommitSeq() != ids.CommitSeq(^uint64(0)) {
		t.Fatal("savepoint control advanced exhausted counters")
	}
	controlsRequire(t, a, "ROLLBACK")
}

func TestSavepointsErrorRecovery(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	controlsRequire(t, a, "SAVEPOINT s")
	ref, ticket := a.Transactions.ref, a.Transactions.ticket
	controlsRequire(t, a, "SAVEPOINT child")
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	controlsRequire(t, a, "RELEASE child")
	controlsAbort(t, a)
	if a.TransactionState() != TxAborted || a.Transactions.ref != ref || a.Transactions.ticket != ticket || len(a.Transactions.savepoints) != 1 {
		t.Fatal("recoverable failure lost ancestor authority")
	}
	if err := controlsDescribe(t, a, "ROLLBACK TO s"); err != nil || a.TransactionState() != TxAborted {
		t.Fatal("Describe recovered or rejected rollback", err)
	}
	for range 2 {
		if err := controlsExecute(t, a, "ROLLBACK TO missing"); err == nil {
			t.Fatal("unknown mark accepted")
		}
		if a.TransactionState() != TxAborted || len(a.Transactions.savepoints) != 1 {
			t.Fatal("failed recovery destroyed ancestor")
		}
	}
	controlsRequire(t, a, "ROLLBACK TO s")
	transactionRows(t, a, "SELECT v FROM t", 1)
	controlsRequire(t, a, "ROLLBACK TO s")
	transactionRows(t, a, "SELECT v FROM t", 1)
	sessionSQL(t, a, "INSERT INTO t VALUES(3)")
	if !transactionEnd(t, a, ref, true).Committed {
		t.Fatal("Go ref did not survive rollback-to")
	}
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 3)
}
