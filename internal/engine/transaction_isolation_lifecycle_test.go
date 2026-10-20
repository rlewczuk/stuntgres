package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTransactionIsolationRetainedResults(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "abort", "physical", "reset", "restore"} {
		t.Run(end, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			ctx := context.Background()
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "INSERT INTO t VALUES(1)")
			sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			op := transactionOperation(t, a)
			result, err := e.Execute(op, statement(t, "SELECT v FROM t"), 0, nil)
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			defer result.Rows.Close()
			sessionSQL(t, b, "UPDATE t SET v=2")
			sessionSQL(t, a, "INSERT INTO t VALUES(3)")
			var dump bytes.Buffer
			if err := e.Dump(ctx, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "VALUES (3)") {
				t.Fatal("Dump leaked private row")
			}
			switch end {
			case "abort":
				op := transactionOperation(t, a)
				_, err := e.Execute(op, statement(t, "UPDATE t SET v=4 WHERE v=1"), 0, nil)
				op.Done()
				waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent update")
				sessionSQL(t, a, "ROLLBACK")
			case "physical":
				a.RequestEnd(session.CleanClose)
				if err := a.WaitEnd(ctx); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if err := e.Reset(ctx); err != nil {
					t.Fatal(err)
				}
				sessionSQL(t, a, "ROLLBACK")
			case "restore":
				if err := e.Restore(ctx, strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(9);")); err != nil {
					t.Fatal(err)
				}
				sessionSQL(t, a, "ROLLBACK")
			default:
				sessionSQL(t, a, end)
				if end == "COMMIT AND CHAIN" {
					sessionSQL(t, a, "ROLLBACK")
				}
			}
			frame := make([]sqltype.Value, 1)
			ok, err := result.Rows.Next(ctx, frame)
			if err != nil || !ok || testExport(frame[0]) != int64(1) {
				t.Fatal(ok, frame, err)
			}
			ok, err = result.Rows.Next(ctx, frame)
			if err != nil || ok {
				t.Fatal("retained result changed", ok, err)
			}
			a.Transactions.Mu.Lock()
			retained := a.Transactions.Roots != nil
			a.Transactions.Mu.Unlock()
			if retained {
				t.Fatal("owner roots retained after disposal")
			}
		})
	}
}
func TestTransactionIsolationSavedShapeFence(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "CREATE TABLE u(v INT,w INT)")
	sessionSQL(t, b, "INSERT INTO u VALUES(1,2)")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	transactionRows(t, a, "SELECT v FROM t")
	sessionSQL(t, b, "COMMENT ON TABLE u IS 'compatible'")
	transactionRows(t, a, "SELECT v FROM u", 1)
	sessionSQL(t, a, "ROLLBACK")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	transactionRows(t, a, "SELECT v FROM t")
	sessionSQL(t, b, "ALTER TABLE u DROP w")
	op := transactionOperation(t, a)
	_, err := e.Execute(op, statement(t, "SELECT v FROM u"), 0, nil)
	op.Done()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("saved shape silently rebound", err)
	}
	sessionSQL(t, a, "ROLLBACK")
	lockEmpty(t, e)
}

func TestTransactionIsolationAbortCharacteristics(t *testing.T) {
	for _, baseline := range []struct {
		mode      string
		isolation session.Isolation
		readOnly  bool
	}{
		{"READ COMMITTED READ WRITE", session.ReadCommitted, false},
		{"REPEATABLE READ READ ONLY", session.RepeatableRead, true},
		{"READ UNCOMMITTED READ ONLY", session.ReadUncommitted, true},
	} {
		for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
			t.Run(baseline.mode+"/"+end, func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				sessionSQL(t, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL "+baseline.mode)
				sessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE")
				sessionSQL(t, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ WRITE")
				op := transactionOperation(t, a)
				result, err := e.Execute(op, statement(t, "SELECT num_nonnulls(1)"), 0, nil)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				if err := result.Rows.Close(); err != nil {
					t.Fatal(err)
				}
				op = transactionOperation(t, a)
				_, err = e.Execute(op, statement(t, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"), 0, nil)
				op.Done()
				waitsDiagnostic(t, err, "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")
				owner := a.Transactions
				owner.Mu.Lock()
				state, isolation, readOnly := owner.State, owner.Isolation, owner.ReadOnly
				owner.Mu.Unlock()
				if state != session.TxAborted || isolation != baseline.isolation || readOnly != baseline.readOnly {
					t.Fatal(state, isolation, readOnly)
				}
				a.Settings.Mu.Lock()
				defaultIsolation := isolationFromText(a.Settings.Value("default_transaction_isolation"))
				defaultReadOnly := a.Settings.Value("default_transaction_read_only") == "on"
				projectedIsolation, projectedReadOnly := a.Settings.CurrentIsolation, a.Settings.CurrentReadOnly
				a.Settings.Mu.Unlock()
				if defaultIsolation != baseline.isolation || defaultReadOnly != baseline.readOnly ||
					projectedIsolation != isolationText(baseline.isolation) || projectedReadOnly != baseline.readOnly {
					t.Fatal(defaultIsolation, defaultReadOnly, projectedIsolation, projectedReadOnly)
				}
				sessionSQL(t, a, end)
				owner.Mu.Lock()
				state, isolation, readOnly = owner.State, owner.Isolation, owner.ReadOnly
				owner.Mu.Unlock()
				if state != session.TxActive || isolation != baseline.isolation || readOnly != baseline.readOnly {
					t.Fatal(state, isolation, readOnly)
				}
				sessionSQL(t, a, "ROLLBACK")
				lockEmpty(t, e)
			})
		}
	}
}

func TestTransactionIsolationImmediateConflictPrecedence(t *testing.T) {
	for _, peer := range []string{"UPDATE t SET v=20", "DELETE FROM t", "UPDATE then DELETE"} {
		for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
			t.Run(peer+"/"+own, func(t *testing.T) {
				e := New()
				a := sessionStart(t, e, nil)
				b := sessionStart(t, e, nil)
				sessionSQL(t, b, "CREATE TABLE t(v INT)")
				sessionSQL(t, b, "INSERT INTO t VALUES(10)")
				sessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				transactionRows(t, a, "SELECT v FROM t", 10)
				if peer == "UPDATE then DELETE" {
					sessionSQL(t, b, "UPDATE t SET v=20")
					sessionSQL(t, b, "DELETE FROM t")
				} else {
					sessionSQL(t, b, peer)
				}
				op := transactionOperation(t, a)
				_, err := e.Execute(op, statement(t, own), 0, nil)
				op.Done()
				word := "update"
				if peer == "DELETE FROM t" {
					word = "delete"
				}
				waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent "+word)
				sessionSQL(t, a, "ROLLBACK")
				lockEmpty(t, e)
			})
		}
	}
}
