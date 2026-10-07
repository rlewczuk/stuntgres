package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestTransactionIsolationVisibility(t *testing.T) {
	for _, mode := range []struct {
		name      string
		isolation string
		stable    bool
	}{
		{name: "RC", isolation: "READ COMMITTED"},
		{name: "RU", isolation: "READ UNCOMMITTED"},
		{name: "RR", isolation: "REPEATABLE READ", stable: true},
	} {
		for _, prepared := range []bool{false, true} {
			path := "direct"
			if prepared {
				path = "held_prepared"
			}
			t.Run(mode.name+"/"+path, func(t *testing.T) {
				_, a, b := transactionConnections(t)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
					defer stop()
					a.ExecContext(cleanup, "ROLLBACK")
					b.ExecContext(cleanup, "ROLLBACK")
				})
				for _, table := range []string{"t", "u", "e", "r", "d"} {
					transactionAffected(t, ctx, b, "CREATE TABLE "+table+"(v INT)", false, 0)
				}
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
				transactionAffected(t, ctx, b, "INSERT INTO u VALUES(10)", false, 1)
				transactionAffected(t, ctx, b, "INSERT INTO r VALUES(10)", false, 1)
				transactionAffected(t, ctx, b, "INSERT INTO d VALUES(40)", false, 1)

				// Hold only t's statement before BEGIN. Preparing the other relations
				// in the block would defeat the first-ever relation-read witness.
				var held *sql.Stmt
				if prepared {
					var err error
					held, err = a.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				selectT := func(want ...int64) {
					t.Helper()
					if held == nil {
						transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, want...)
						return
					}
					rows, err := held.QueryContext(ctx)
					if err != nil {
						t.Fatal(err)
					}
					transactionValues(t, rows, want...)
				}

				transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL "+mode.isolation, false, 0)
				// BEGIN must not fix RR's snapshot: this earlier peer commit is included.
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(2)", false, 1)
				selectT(1, 2)

				transactionAffected(t, ctx, b, "BEGIN", false, 0)
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(3)", false, 1)
				transactionAffected(t, ctx, b, "INSERT INTO u VALUES(20)", false, 1)
				transactionAffected(t, ctx, b, "INSERT INTO e VALUES(30)", false, 1)
				transactionAffected(t, ctx, b, "UPDATE r SET v=20 WHERE v=10", false, 1)
				transactionAffected(t, ctx, b, "DELETE FROM d WHERE v=40", false, 1)
				selectT(1, 2) // Even RU must not expose uncommitted peer rows.
				transactionAffected(t, ctx, b, "COMMIT", false, 0)

				if mode.stable {
					selectT(1, 2)
					transactionSelect(t, ctx, a, "SELECT v FROM u ORDER BY v", false, 10)
					transactionSelect(t, ctx, a, "SELECT v FROM e ORDER BY v", false)
					// r was never read or prepared: retain its replaced old root too.
					transactionSelect(t, ctx, a, "SELECT v FROM r ORDER BY v", false, 10)
					transactionSelect(t, ctx, a, "SELECT v FROM d ORDER BY v", false, 40)
				} else {
					selectT(1, 2, 3)
					transactionSelect(t, ctx, a, "SELECT v FROM u ORDER BY v", false, 10, 20)
					transactionSelect(t, ctx, a, "SELECT v FROM e ORDER BY v", false, 30)
					transactionSelect(t, ctx, a, "SELECT v FROM r ORDER BY v", false, 20)
					transactionSelect(t, ctx, a, "SELECT v FROM d ORDER BY v", false)
				}

				transactionAffected(t, ctx, a, "INSERT INTO t VALUES(4)", false, 1)
				if mode.stable {
					selectT(1, 2, 4)
				} else {
					selectT(1, 2, 3, 4)
				}
				transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
				transactionAffected(t, ctx, a, "COMMIT", false, 0)
				transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 2, 3, 4)
			})
		}
	}

	for _, prepared := range []bool{false, true} {
		path := "direct"
		if prepared {
			path = "prepared"
		}
		t.Run("RR/concurrent_update/"+path, func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				a.ExecContext(cleanup, "ROLLBACK")
			})
			transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(10)", false, 1)
			transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
			transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 10)
			transactionAffected(t, ctx, b, "UPDATE t SET v=20 WHERE v=10", false, 1)
			// The existing helper requires successful Prepare before actual Exec.
			err := transactionConstraintExec(t, ctx, a, "UPDATE t SET v=30", prepared)
			transactionDiagnostic(t, err, controlsDiagnostic("40001", "could not serialize access due to concurrent update"))
			_, err = a.ExecContext(ctx, "SELECT v FROM t ORDER BY v")
			transactionDiagnostic(t, err, controlsAborted())
			transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 20)
		})
	}
}
