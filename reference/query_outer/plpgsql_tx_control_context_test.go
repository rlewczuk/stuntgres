package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePLpgSQLTxControlCallStack(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		txControlSetup(t, p, b, c)
		resultsExec(t, p, b, c,
			"CREATE FUNCTION tx_argument() RETURNS integer LANGUAGE SQL AS 'SELECT 0'",
			"CREATE PROCEDURE tx_middle() LANGUAGE plpgsql AS $$BEGIN CALL tx_segments(0);END$$",
			"CREATE PROCEDURE tx_outer() LANGUAGE plpgsql AS $$BEGIN PERFORM 1;CALL tx_middle();END$$",
			"CREATE PROCEDURE tx_inline() LANGUAGE plpgsql AS $outer$BEGIN DO $$BEGIN CALL tx_segments(0);END$$;END$outer$",
			"CREATE FUNCTION tx_barrier() RETURNS integer LANGUAGE plpgsql AS $$BEGIN CALL tx_segments(0);RETURN 1;END$$",
			"CREATE PROCEDURE tx_via_function() LANGUAGE plpgsql AS $$BEGIN PERFORM tx_barrier();END$$",
			"CREATE PROCEDURE tx_dynamic() LANGUAGE plpgsql AS $$BEGIN EXECUTE 'CALL tx_segments(0)';END$$")
		for _, q := range []string{"CALL tx_outer()", "CALL tx_inline()", "CALL tx_segments(tx_argument())"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec(q, 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				txControlRows(t, p, b, c, 1, 3)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 2))
			}
		}
		for _, q := range []string{"SELECT tx_barrier()", "CALL tx_via_function()", "CALL tx_dynamic()"} {
			for _, prepared := range []bool{false, true} {
				var err error
				if prepared {
					stmt, e := c.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = stmt.ExecContext(p.Ctx)
					stmt.Close()
				} else {
					_, err = c.ExecContext(p.Ctx, q)
				}
				want := proceduresDiagnostic("2D000", "invalid transaction termination", 0)
				want.Context = "PL/pgSQL function tx_segments(integer) line 1 at COMMIT"
				if q == "CALL tx_dynamic()" {
					want.Context += "\nSQL statement \"CALL tx_segments(0)\"\nPL/pgSQL function tx_dynamic() line 1 at EXECUTE"
				} else {
					want.Context += "\nSQL statement \"CALL tx_segments(0)\"\nPL/pgSQL function tx_barrier() line 1 at CALL"
					if q == "CALL tx_via_function()" {
						want.Context += "\nSQL statement \"SELECT tx_barrier()\"\nPL/pgSQL function tx_via_function() line 1 at PERFORM"
					}
				}
				fixture.AssertDiagnostic(t, err, &want)
				txControlRows(t, p, b, c)
			}
		}
		for _, command := range []string{"COMMIT", "ROLLBACK"} {
			resultsExec(t, p, b, c, "CREATE OR REPLACE PROCEDURE tx_dynamic_end() LANGUAGE plpgsql AS $$BEGIN EXECUTE '"+command+"';END$$")
			want := proceduresDiagnostic("0A000", "EXECUTE of transaction commands is not implemented", 0)
			want.Context = "PL/pgSQL function tx_dynamic_end() line 1 at EXECUTE"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CALL tx_dynamic_end()", want)
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		resultsExec(t, p, b, c, "CREATE PROCEDURE tx_dispatch(IN n integer) LANGUAGE plpgsql AS $$BEGIN IF n<0 THEN PERFORM tx_barrier();ELSE CALL tx_segments(n);END IF;END$$")
		q := "CALL tx_dispatch($1::integer)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		txControlRows(t, p, b, c)
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0, 0))
		want := proceduresDiagnostic("2D000", "invalid transaction termination", 0)
		want.Context = "PL/pgSQL function tx_segments(integer) line 1 at COMMIT\nSQL statement \"CALL tx_segments(0)\"\nPL/pgSQL function tx_barrier() line 1 at CALL\nSQL statement \"SELECT tx_barrier()\"\nPL/pgSQL function tx_dispatch(integer) line 1 at PERFORM"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, -1))
		txControlRows(t, p, b, c, 1, 3)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0, 10))
		txControlRows(t, p, b, c, 1, 3, 11, 13)
	})
}

func TestReferencePLpgSQLTxControlSQLBodyContext(t *testing.T) {
	for _, command := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(command, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				txControlSetup(t, p, b, c)
				resultsExec(t, p, b, c,
					"CREATE PROCEDURE tx_sql_inner(IN terminate boolean) LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(1);IF terminate THEN "+command+";END IF;INSERT INTO tx_log VALUES(2);END$$",
					"CREATE PROCEDURE tx_sql_outer(IN terminate boolean) LANGUAGE SQL AS 'CALL tx_sql_inner($1)'",
					"CREATE PROCEDURE tx_sql_completed() LANGUAGE plpgsql AS $$BEGIN CALL tx_sql_outer(false);CALL tx_segments(10);END$$",
					"CREATE PROCEDURE tx_sql_caught() LANGUAGE plpgsql AS $$BEGIN BEGIN CALL tx_sql_outer(true);EXCEPTION WHEN invalid_transaction_termination THEN PERFORM 1;END;CALL tx_segments(20);END$$")
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_log VALUES(40)", 1))
					q := "CALL tx_sql_outer($1::boolean)"
					target := fixture.Executor(c)
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						target = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						txControlRows(t, p, b, c, 40)
					}
					want := proceduresDiagnostic("2D000", "invalid transaction termination", 0)
					want.Context = "PL/pgSQL function tx_sql_inner(boolean) line 1 at " + command + "\nSQL function \"tx_sql_outer\" statement 1"
					// A held definition alternates refusal and a dead-control
					// success; neither Prepare nor a failed CALL publishes rows.
					for range 2 {
						fixture.RunStep(t, p.Ctx, target, b.Recorder, fixture.ErrorDiagnostic(q, want, true))
						txControlRows(t, p, b, c, 40)
						fixture.RunStep(t, p.Ctx, target, b.Recorder, fixture.Exec(q, 0, false))
						txControlRows(t, p, b, c, 1, 2, 40)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log WHERE v=1", 1))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log WHERE v=2", 1))
					}
					if stmt != nil {
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					// Restore eligibility within the same PL invocation after
					// both a completed SQL body and a caught SQL-body refusal.
					directRows := []int64{1, 2, 40}
					if command == "ROLLBACK" {
						directRows = []int64{2, 40}
					}
					for _, recovery := range []struct {
						query string
						rows  []int64
					}{
						{"CALL tx_sql_completed()", []int64{1, 2, 11, 13, 40}},
						{"CALL tx_sql_caught()", []int64{21, 23, 40}},
						{"CALL tx_segments(30)", []int64{31, 33, 40}},
						{"CALL tx_sql_inner(true)", directRows},
						{"DO $$BEGIN CALL tx_sql_inner(true);END$$", directRows},
					} {
						step := fixture.Exec(recovery.query, 0)
						if prepared {
							step = fixture.Prepared(step)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						txControlRows(t, p, b, c, recovery.rows...)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", int64(len(recovery.rows))))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_log VALUES(40)", 1))
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 1))
				}
			})
		})
	}
}

func TestReferencePLpgSQLTxControlRestrictions(t *testing.T) {
	for _, command := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(command+"/caught_and_enclosing_marks", func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_caught(INOUT n integer) LANGUAGE plpgsql AS $$BEGIN BEGIN n:=n+1;INSERT INTO tx_log VALUES(1);"+command+";EXCEPTION WHEN invalid_transaction_termination THEN INSERT INTO tx_log VALUES(n);END;INSERT INTO tx_log VALUES(12);END$$", "CREATE PROCEDURE tx_nested_handler() LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(1);BEGIN PERFORM 1/0;EXCEPTION WHEN division_by_zero THEN "+command+";END;EXCEPTION WHEN division_by_zero THEN PERFORM 1;END$$")
				for _, prepared := range []bool{false, true} {
					s := proceduresStep(b, "CALL tx_caught(10)", []string{"n"}, []string{"INT4"}, [][]any{{int64(11)}})
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					txControlRows(t, p, b, c, 11, 12)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 2))
					verb := "commit"
					if command == "ROLLBACK" {
						verb = "roll back"
					}
					want := proceduresDiagnostic("2D000", "cannot "+verb+" while a subtransaction is active", 0)
					want.Context = "PL/pgSQL function tx_nested_handler() line 1 at " + command
					failure := fixture.ErrorDiagnostic("CALL tx_nested_handler()", want)
					if prepared {
						failure = fixture.Prepared(failure)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
					txControlRows(t, p, b, c)
				}
			})
		})
		for _, atomic := range []bool{false, true} {
			t.Run(command+"/precedence/"+map[bool]string{false: "protected", true: "atomic"}[atomic], func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					attributes := ""
					if atomic {
						attributes = " SECURITY DEFINER"
					}
					resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_precedence() LANGUAGE plpgsql"+attributes+" AS $$DECLARE r record;q record;BEGIN FOR r IN UPDATE tx_log SET v=v+1 RETURNING v LOOP FOR q IN SELECT x,1/(x-1000) AS bad FROM generate_series(1,1000) x LOOP "+command+";END LOOP;END LOOP;EXCEPTION WHEN division_by_zero THEN PERFORM 1;END$$")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_log VALUES(1)", 1))
					verb := "commit"
					if command == "ROLLBACK" {
						verb = "roll back"
					}
					message := "cannot " + verb + " while a subtransaction is active"
					if atomic {
						message = "invalid transaction termination"
					}
					want := proceduresDiagnostic("2D000", message, 0)
					want.Context = "PL/pgSQL function tx_precedence() line 1 at " + command
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("CALL tx_precedence()", want)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						txControlRows(t, p, b, c, 1)
					}
				})
			})
		}
		t.Run(command+"/inline_explicit", func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)")
				q := "DO $$BEGIN INSERT INTO tx_log VALUES(1);" + command + ";INSERT INTO tx_log VALUES(2);END$$"
				want := proceduresDiagnostic("2D000", "invalid transaction termination", 0)
				want.Context = "PL/pgSQL function inline_code_block line 1 at " + command
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
					s := fixture.ErrorDiagnostic(q, want)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", proceduresDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block", 0)))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
					txControlRows(t, p, b, c)
					cached, err := c.PrepareContext(p.Ctx, "SELECT 1")
					if err != nil {
						t.Fatal(err)
					}
					defer cached.Close()
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						cached.Close()
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
					aborted := proceduresDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block", 0)
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", aborted))
					failedPrepare := fixture.Prepared(fixture.ErrorDiagnostic("SELECT 1", aborted))
					failedPrepare.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, failedPrepare)
					inherited := tx.StmtContext(p.Ctx, cached)
					defer inherited.Close()
					_, err = inherited.ExecContext(p.Ctx)
					fixture.AssertDiagnostic(t, err, &aborted)
					inherited.Close()
					cached.Close()
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					txControlRows(t, p, b, c)
				}
			})
		})
		for _, barrier := range []string{"SECURITY DEFINER", "SET application_name TO 'nested'", "EXCEPTION"} {
			t.Run(command+"/nested/"+barrier, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_inner() LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(1);"+command+";INSERT INTO tx_log VALUES(2);END$$")
					attributes, suffix := barrier, ""
					if barrier == "EXCEPTION" {
						attributes = ""
						suffix = " EXCEPTION WHEN division_by_zero THEN PERFORM 1;"
					}
					resultsExec(t, p, b, c, "CREATE PROCEDURE tx_guard() LANGUAGE plpgsql "+attributes+" AS $$BEGIN CALL tx_inner();"+suffix+"END$$")
					want := proceduresDiagnostic("2D000", "invalid transaction termination", 0)
					want.Context = "PL/pgSQL function tx_inner() line 1 at " + command + "\nSQL statement \"CALL tx_inner()\"\nPL/pgSQL function tx_guard() line 1 at CALL"
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("CALL tx_guard()", want)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						txControlRows(t, p, b, c)
					}
				})
			})
		}
		for _, restriction := range []string{"explicit", "definer", "configuration", "function", "exception"} {
			t.Run(command+"/"+restriction, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)")
					attributes := ""
					prefix := "CREATE PROCEDURE tx_restricted()"
					suffix := ""
					if restriction == "definer" {
						attributes = " SECURITY DEFINER"
					}
					if restriction == "configuration" {
						attributes = " SET application_name TO 'tx-test'"
					}
					if restriction == "function" {
						prefix = "CREATE FUNCTION tx_restricted() RETURNS void"
					}
					if restriction == "exception" {
						suffix = " EXCEPTION WHEN division_by_zero THEN PERFORM 1;"
					}
					resultsExec(t, p, b, c, prefix+" LANGUAGE plpgsql"+attributes+" AS $$BEGIN INSERT INTO tx_log VALUES(1);"+command+";INSERT INTO tx_log VALUES(2);"+suffix+"END$$")
					message := "invalid transaction termination"
					if restriction == "exception" {
						verb := "commit"
						if command == "ROLLBACK" {
							verb = "roll back"
						}
						message = "cannot " + verb + " while a subtransaction is active"
					}
					want := proceduresDiagnostic("2D000", message, 0)
					want.Context = "PL/pgSQL function tx_restricted() line 1 at " + command
					q := "CALL tx_restricted()"
					if restriction == "function" {
						q = "SELECT tx_restricted()"
					}
					for _, prepared := range []bool{false, true} {
						target := fixture.Executor(c)
						var tx *sql.Tx
						if restriction == "explicit" {
							var err error
							tx, err = c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							target = tx
						}
						s := fixture.ErrorDiagnostic(q, want)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, target, b.Recorder, s)
						if tx != nil {
							fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", proceduresDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block", 0)))
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
						}
						txControlRows(t, p, b, c)
					}
					// A handled exception has consumed its internal mark before its handler.
					resultsExec(t, p, b, c, "CREATE PROCEDURE tx_handler() LANGUAGE plpgsql AS $$BEGIN BEGIN INSERT INTO tx_log VALUES(5);PERFORM 1/0;EXCEPTION WHEN division_by_zero THEN INSERT INTO tx_log VALUES(6);"+command+";END;INSERT INTO tx_log VALUES(7);END$$")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CALL tx_handler()", 0))
					if command == "COMMIT" {
						txControlRows(t, p, b, c, 6, 7)
					} else {
						txControlRows(t, p, b, c, 7)
					}
				})
			})
		}
	}
}
