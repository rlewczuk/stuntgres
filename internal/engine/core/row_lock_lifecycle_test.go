package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestRowLockRepeatableRead(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
			for _, commit := range []bool{false, true} {
				t.Run(clause+"/"+change+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
					e, a, b, _ := rowLockEngineFixture(t)
					sessionSQL(t, b, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					transactionRows(t, b, "SELECT v FROM t WHERE id=1", 10)
					ar := transactionBegin(t, a)
					transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
					sessionSQL(t, a, change)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					done := deleteAsync(t, b, ctx, "SELECT v FROM t WHERE id=1 "+clause)
					rowLockQueued(t, e, a.Transactions.Ticket, analyze.RowLockMode(clause))
					transactionEnd(t, a, ar, commit)
					if commit && !(clause == "FOR KEY SHARE" && change[0] == 'U') {
						select {
						case outcome := <-done:
							if outcome.result.Rows != nil {
								outcome.result.Rows.Close()
								t.Fatal("error exposed cursor")
							}
							waitsDiagnostic(t, outcome.err, "40001", "could not serialize access due to concurrent update")
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						}
					} else {
						rowLockAwait(t, done, 10)
					}
					sessionSQL(t, b, "ROLLBACK")
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
}
func TestRowLockControls(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
			e, a, b, c := rowLockEngineFixture(t)
			ar := transactionBegin(t, a)
			retained := sessionSQL(t, c, "SELECT v FROM t ORDER BY v FOR KEY SHARE")
			defer retained.Rows.Close()
			transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := deleteAsync(t, b, ctx, "SELECT v FROM t ORDER BY v FOR SHARE")
			rowLockQueued(t, e, a.Transactions.Ticket, lock.ForShare)
			if restore {
				if err := engine.Restore(ctx, e, strings.NewReader("CREATE TABLE t(id INT,v INT);INSERT INTO t VALUES(1,99)")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := engine.Reset(ctx, e); err != nil {
					t.Fatal(err)
				}
				sessionSQL(t, c, "CREATE TABLE t(id INT,v INT)")
				sessionSQL(t, c, "INSERT INTO t VALUES(1,99)")
			}
			select {
			case outcome := <-done:
				if !errors.Is(outcome.err, ids.ErrCatalogReplaced) || outcome.result.Rows != nil {
					t.Fatal(outcome)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if got := enginetest.Tuples(retained); len(got) != 2 || got[0][0] != int64(10) || got[1][0] != int64(20) {
				t.Fatal("retained locker changed", got)
			}
			transactionEnd(t, a, ar, false)
			enginetest.LockEmpty(t, e)
			deleteReplacementAdmissionEmpty(t, e)
			transactionRows(t, b, "SELECT v FROM t FOR UPDATE", 99)
		})
	}
	t.Run("immediate_admission", func(t *testing.T) {
		e, _, b, _ := rowLockEngineFixture(t)
		op := transactionOperation(t, b)
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = scope.Finish(errors.New("test cleanup")) }()
		stmt := enginetest.Statement(t, "SELECT v FROM t ORDER BY v FOR SHARE")
		if err = scope.Gate(stmt); err != nil {
			t.Fatal(err)
		}
		ctx := &deleteAdmissionContext{Context: op.Context(), engine: e}
		op.Ctx = ctx
		result, err := scope.Execute(stmt, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := enginetest.Tuples(result)
		if len(got) != 2 || got[0][0] != int64(10) || got[1][0] != int64(20) {
			t.Fatal(got)
		}
		if ctx.lost || ctx.checks == 0 {
			t.Fatal("uncontended statement lost continuous admission", ctx)
		}
		if err = scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		enginetest.LockEmpty(t, e)
		deleteReplacementAdmissionEmpty(t, e)
	})
}
func TestRowLockLifetime(t *testing.T) {
	e, a, b, _ := rowLockEngineFixture(t)
	ar := transactionBegin(t, a)
	transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", 10)
	sessionSQL(t, a, "SAVEPOINT s")
	transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
	transactionRows(t, a, "SELECT v FROM t WHERE id=2 FOR SHARE", 20)
	sessionSQL(t, a, "ROLLBACK TO s")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := deleteAsync(t, b, ctx, "UPDATE t SET v=21 WHERE id=2")
	deleteAwait(t, done, 1, nil)
	transactionRows(t, b, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", 10)
	transactionEnd(t, a, ar, false)
	enginetest.LockEmpty(t, e)
}
func TestRowLockDiagnostics(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		t.Run(clause, func(t *testing.T) {
			e, a, _, _ := rowLockEngineFixture(t)
			sessionSQL(t, a, "BEGIN READ ONLY")
			done := deleteAsync(t, a, context.Background(), "SELECT v FROM t WHERE id=99 "+clause)
			outcome := <-done
			waitsDiagnostic(t, outcome.err, "25006", "cannot execute SELECT "+clause+" in a read-only transaction")
			sessionSQL(t, a, "ROLLBACK")
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("projection_atomicity", func(t *testing.T) {
		e, a, b, _ := rowLockEngineFixture(t)
		sessionSQL(t, a, "CREATE TABLE dest(v INT)")
		done := deleteAsync(t, a, context.Background(), "INSERT INTO dest SELECT 100/(20-v) FROM t ORDER BY id FOR UPDATE")
		outcome := <-done
		waitsDiagnostic(t, outcome.err, "22012", "division by zero")
		transactionRows(t, b, "SELECT v FROM dest")
		sessionSQL(t, b, "UPDATE t SET v=30")
		enginetest.LockEmpty(t, e)
	})
}
