package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestTransactionIsolationRetainedResults(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "abort", "physical", "reset", "restore"} {
		t.Run(end, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			ctx := context.Background()
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1)")
			enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			op := enginetest.TransactionOperation(t, a)
			result, err := e.Execute(op, enginetest.Statement(t, "SELECT v FROM t"), 0, nil)
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			defer result.Rows.Close()
			enginetest.SessionSQL(t, b, "UPDATE t SET v=2")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(3)")
			var dump bytes.Buffer
			if err := engine.Dump(ctx, e, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "VALUES (3)") {
				t.Fatal("Dump leaked private row")
			}
			switch end {
			case "abort":
				op := enginetest.TransactionOperation(t, a)
				_, err := e.Execute(op, enginetest.Statement(t, "UPDATE t SET v=4 WHERE v=1"), 0, nil)
				op.Done()
				waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent update")
				enginetest.SessionSQL(t, a, "ROLLBACK")
			case "physical":
				a.RequestEnd(session.CleanClose)
				if err := a.WaitEnd(ctx); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if err := engine.Reset(ctx, e); err != nil {
					t.Fatal(err)
				}
				enginetest.SessionSQL(t, a, "ROLLBACK")
			case "restore":
				if err := engine.Restore(ctx, e, strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(9);")); err != nil {
					t.Fatal(err)
				}
				enginetest.SessionSQL(t, a, "ROLLBACK")
			default:
				enginetest.SessionSQL(t, a, end)
				if end == "COMMIT AND CHAIN" {
					enginetest.SessionSQL(t, a, "ROLLBACK")
				}
			}
			frame := make([]sqltype.Value, 1)
			ok, err := result.Rows.Next(ctx, frame)
			if err != nil || !ok || enginetest.Export(frame[0]) != int64(1) {
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
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "CREATE TABLE u(v INT,w INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO u VALUES(1,2)")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.TransactionRows(t, a, "SELECT v FROM t")
	enginetest.SessionSQL(t, b, "COMMENT ON TABLE u IS 'compatible'")
	enginetest.TransactionRows(t, a, "SELECT v FROM u", 1)
	enginetest.SessionSQL(t, a, "ROLLBACK")
	enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.TransactionRows(t, a, "SELECT v FROM t")
	enginetest.SessionSQL(t, b, "ALTER TABLE u DROP w")
	op := enginetest.TransactionOperation(t, a)
	_, err := e.Execute(op, enginetest.Statement(t, "SELECT v FROM u"), 0, nil)
	op.Done()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("saved shape silently rebound", err)
	}
	enginetest.SessionSQL(t, a, "ROLLBACK")
	enginetest.LockEmpty(t, e)
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
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL "+baseline.mode)
				enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE")
				enginetest.SessionSQL(t, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ WRITE")
				op := enginetest.TransactionOperation(t, a)
				result, err := e.Execute(op, enginetest.Statement(t, "SELECT num_nonnulls(1)"), 0, nil)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				if err := result.Rows.Close(); err != nil {
					t.Fatal(err)
				}
				op = enginetest.TransactionOperation(t, a)
				_, err = e.Execute(op, enginetest.Statement(t, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"), 0, nil)
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
				defaultIsolation := session.IsolationFromText(a.Settings.Value("default_transaction_isolation"))
				defaultReadOnly := a.Settings.Value("default_transaction_read_only") == "on"
				projectedIsolation, projectedReadOnly := a.Settings.CurrentIsolation, a.Settings.CurrentReadOnly
				a.Settings.Mu.Unlock()
				if defaultIsolation != baseline.isolation || defaultReadOnly != baseline.readOnly ||
					projectedIsolation != session.IsolationText(baseline.isolation) || projectedReadOnly != baseline.readOnly {
					t.Fatal(defaultIsolation, defaultReadOnly, projectedIsolation, projectedReadOnly)
				}
				enginetest.SessionSQL(t, a, end)
				owner.Mu.Lock()
				state, isolation, readOnly = owner.State, owner.Isolation, owner.ReadOnly
				owner.Mu.Unlock()
				if state != session.TxActive || isolation != baseline.isolation || readOnly != baseline.readOnly {
					t.Fatal(state, isolation, readOnly)
				}
				enginetest.SessionSQL(t, a, "ROLLBACK")
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestTransactionIsolationImmediateConflictPrecedence(t *testing.T) {
	for _, peer := range []string{"UPDATE t SET v=20", "DELETE FROM t", "UPDATE then DELETE"} {
		for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
			t.Run(peer+"/"+own, func(t *testing.T) {
				e := engine.New()
				a := enginetest.StartSession(t, e, nil)
				b := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
				enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(10)")
				enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				enginetest.TransactionRows(t, a, "SELECT v FROM t", 10)
				if peer == "UPDATE then DELETE" {
					enginetest.SessionSQL(t, b, "UPDATE t SET v=20")
					enginetest.SessionSQL(t, b, "DELETE FROM t")
				} else {
					enginetest.SessionSQL(t, b, peer)
				}
				op := enginetest.TransactionOperation(t, a)
				_, err := e.Execute(op, enginetest.Statement(t, own), 0, nil)
				op.Done()
				word := "update"
				if peer == "DELETE FROM t" {
					word = "delete"
				}
				waitsDiagnostic(t, err, "40001", "could not serialize access due to concurrent "+word)
				enginetest.SessionSQL(t, a, "ROLLBACK")
				enginetest.LockEmpty(t, e)
			})
		}
	}
}
