package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
)

func TestReferenceSQLCursorSavepoints(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		run := func(q string) { sqlCursorStep(t, p, b, c, referenceExec(q, 0)) }
		declare := func(name string) {
			run("DECLARE " + name + " NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i")
		}
		run("BEGIN")
		declare("parent")
		run("SAVEPOINT s")
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM parent", [][]any{{int64(1), "one"}}, b.pg))
		declare("child")
		run("ROLLBACK TO s")
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM parent", [][]any{{int64(2), nil}}, b.pg))
		sqlCursorError(t, p, b, c, "FETCH ALL FROM child", sqlCursorMissing("child"))
		// An unrelated recovered error does not poison or rewind an older portal.
		sqlCursorError(t, p, b, c, "SELECT 1/0", sqlCursorDiagnostic("22012", "division by zero"))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM parent", [][]any{{int64(3), "three"}}, b.pg))
		run("SAVEPOINT closing")
		run("CLOSE parent")
		run("ROLLBACK TO closing")
		sqlCursorError(t, p, b, c, "FETCH ALL FROM parent", sqlCursorMissing("parent"))
		run("SAVEPOINT outer_s")
		run("SAVEPOINT inner_s")
		declare("released")
		run("RELEASE inner_s")
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM released", [][]any{{int64(1), "one"}}, b.pg))
		run("ROLLBACK TO outer_s")
		sqlCursorError(t, p, b, c, "FETCH ALL FROM released", sqlCursorMissing("released"))
		run("SAVEPOINT failed_child")
		run("DECLARE bad NO SCROLL CURSOR FOR SELECT 10/(i-1) AS n FROM cursor_rows ORDER BY i")
		sqlCursorStep(t, p, b, c, referenceErrorDiagnostic("FETCH 1 FROM bad", sqlCursorDiagnostic("22012", "division by zero")))
		run("ROLLBACK TO failed_child")
		sqlCursorError(t, p, b, c, "FETCH ALL FROM bad", sqlCursorMissing("bad"))
		declare("healthy")
		declare("failed")
		want := sqlCursorDiagnostic("55000", "cursor can only scan forward")
		want.Hint = "Declare it with SCROLL option to enable backward scan."
		sqlCursorError(t, p, b, c, "FETCH BACKWARD 1 FROM failed", want)
		run("CLOSE ALL")
		run("CLOSE ALL")
		for _, name := range []string{"healthy", "failed"} {
			sqlCursorError(t, p, b, c, "FETCH ALL FROM "+name, sqlCursorMissing(name))
		}
	})
}

func TestReferenceSQLCursorTransactionEnds(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "driver-commit", "driver-rollback", "PREPARE TRANSACTION 'cursor_end'", "abort"} {
		t.Run(end, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				var executor referenceExecutor = c
				var tx *sql.Tx
				if end == "driver-commit" || end == "driver-rollback" {
					var err error
					tx, err = c.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					executor = tx
				} else {
					sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
				}
				sqlCursorStep(t, p, b, executor, referenceExec("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
				switch end {
				case "driver-commit":
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				case "driver-rollback":
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				case "abort":
					sqlCursorStep(t, p, b, c, referenceErrorDiagnostic("SELECT 1/0", sqlCursorDiagnostic("22012", "division by zero")))
					sqlCursorStep(t, p, b, c, referenceErrorDiagnostic("FETCH ALL FROM c", sqlCursorDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK", 0))
				default:
					sqlCursorStep(t, p, b, c, referenceExec(end, 0))
				}
				if end == "PREPARE TRANSACTION 'cursor_end'" {
					defer c.ExecContext(context.Background(), "ROLLBACK PREPARED 'cursor_end'")
				}
				chained := end == "COMMIT AND CHAIN" || end == "ROLLBACK AND CHAIN"
				if chained {
					sqlCursorError(t, p, b, c, "FETCH ALL FROM c", sqlCursorMissing("c"))
				} else {
					sqlCursorStep(t, p, b, c, referenceErrorDiagnostic("FETCH ALL FROM c", sqlCursorMissing("c")))
				}
				if !chained {
					sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
				}
				sqlCursorStep(t, p, b, c, referenceExec("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
				sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM c", [][]any{{int64(1), "one"}}, b.pg))
				sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK", 0))
				if end == "PREPARE TRANSACTION 'cursor_end'" {
					sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK PREPARED 'cursor_end'", 0))
				}
			})
		})
	}
}

func TestReferenceSQLCursorCloseAndSession(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		peer, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(p.ctx, "ROLLBACK")
		sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
		sqlCursorStep(t, p, b, peer, referenceErrorDiagnostic("FETCH ALL FROM c", sqlCursorMissing("c")))
		sqlCursorStep(t, p, b, peer, referenceExec("BEGIN", 0))
		sqlCursorStep(t, p, b, peer, referenceExec("DECLARE c NO SCROLL CURSOR FOR SELECT 99 AS peer", 0))
		sqlCursorStep(t, p, b, peer, referenceSelect("FETCH ALL FROM c", []string{"peer"}, []string{"INT4"}, [][]any{{int64(99)}}))
		rows, err := c.QueryContext(p.ctx, "FETCH 1 FROM c")
		if err != nil {
			t.Fatal(err)
		}
		driverBasicMetadata(t, rows, b.pg, []string{"INT4", "TEXT"}, []bool{false, true})
		// Closing an unread public batch discards just that one batch.
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM c", [][]any{{int64(2), nil}}, b.pg))
		sqlCursorStep(t, p, b, c, referenceExec("CLOSE c", 0))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE c NO SCROLL CURSOR FOR SELECT 'fresh'::TEXT AS fresh", 0))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH ALL FROM c", []string{"fresh"}, []string{"TEXT"}, [][]any{{"fresh"}}))
		// ErrBadConn through Raw explicitly discards the physical connection;
		// merely calling sql.Conn.Close would only return a pool borrower.
		if err := c.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
			t.Fatalf("physical discard: %v", err)
		}
		replacement, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer replacement.Close()
		sqlCursorStep(t, p, b, replacement, referenceErrorDiagnostic("FETCH ALL FROM c", sqlCursorMissing("c")))
	})
}
