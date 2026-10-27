package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func txControlNestedHolding(t *testing.T) {
	for _, bound := range []bool{false, true} {
		name := "implicit"
		declarations := "DECLARE r record;q record;"
		outer, inner := "SELECT v FROM tx_source ORDER BY v", "SELECT v FROM tx_source ORDER BY v"
		if bound {
			name = "bound"
			declarations = "DECLARE tx_outer CURSOR FOR SELECT v FROM tx_source ORDER BY v;tx_inner CURSOR FOR SELECT v FROM tx_source ORDER BY v;"
			outer, inner = "tx_outer", "tx_inner"
		}
		t.Run(name+"/nested_mixed", func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE tx_source(v integer)", "CREATE PROCEDURE tx_nested_hold(OUT visits text) LANGUAGE plpgsql AS $$"+declarations+"BEGIN FOR r IN "+outer+" LOOP FOR q IN "+inner+" LOOP visits:=coalesce(visits,'')||r.v::text||q.v::text;IF r.v=1 AND q.v=1 THEN COMMIT;DELETE FROM tx_source;INSERT INTO tx_source VALUES(4);ELSE ROLLBACK;END IF;END LOOP;END LOOP;END$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_source VALUES(1),(2)", 2))
				proceduresBoth(t, p, b, c, "CALL tx_nested_hold(NULL)", []string{"visits"}, []string{"TEXT"}, [][]any{{"11122122"}})
				resultsStoredBoth(t, p, b, c, "SELECT v FROM tx_source ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
				resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name IN ('tx_outer','tx_inner')", 0)
			})
		})
	}
}

func txControlEnclosingWritable(t *testing.T, command string) {
	for _, late := range []bool{false, true} {
		name, source := "enclosing_writable", "SELECT x FROM generate_series(1,3) x"
		if late {
			name, source = "enclosing_writable_late", "SELECT x,1/(x-1000) AS bad FROM generate_series(1,1000) x"
		}
		t.Run(command+"/"+name, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_outer_write() LANGUAGE plpgsql AS $$DECLARE r record;q record;BEGIN FOR r IN UPDATE tx_log SET v=v+1 RETURNING v LOOP FOR q IN "+source+" LOOP "+command+";END LOOP;END LOOP;END$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_log VALUES(1)", 1))
				want := proceduresDiagnostic("55000", "cannot perform transaction commands inside a cursor loop that is not read-only", 0)
				lateWant := proceduresDiagnostic("22012", "division by zero", 0)
				lateWant.Context = "PL/pgSQL function tx_outer_write() line 1 at " + command
				want.Context = "PL/pgSQL function tx_outer_write() line 1 at " + command
				for _, prepared := range []bool{false, true} {
					if late {
						// REL_18_6 HoldPinnedPortals checks then holds each portal
						// in hash order. Either independent fault may be first;
						// neither permits publication or any other diagnostic.
						func() {
							const query = "CALL tx_outer_write()"
							var result sql.Result
							var err error
							if prepared {
								stmt, prepareErr := c.PrepareContext(p.Ctx, query)
								if prepareErr != nil {
									t.Fatal(prepareErr)
								}
								defer stmt.Close()
								result, err = stmt.ExecContext(p.Ctx)
							} else {
								result, err = c.ExecContext(p.Ctx, query)
							}
							if err == nil || result != nil {
								t.Fatalf("competing cursor faults returned result %v, error %v", result, err)
							}
							got := fixture.ExtractDiagnostic(t, err)
							if got != want && got != lateWant {
								t.Fatalf("diagnostic got %#v want either %#v or %#v", got, want, lateWant)
							}
						}()
					} else {
						s := fixture.ErrorDiagnostic("CALL tx_outer_write()", want)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					txControlRows(t, p, b, c, 1)
					resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE statement IN ('UPDATE tx_log SET v=v+1 RETURNING v','"+source+"')", 0)
				}
			})
		})
	}
}

func txControlCursorErrorCancel(t *testing.T) {
	for _, bound := range []bool{false, true} {
		name := "implicit"
		if bound {
			name = "bound"
		}
		t.Run(name+"/error_cancel", func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_cleanup_error() LANGUAGE plpgsql AS $$"+txLoopSource(bound, "SELECT x AS v FROM generate_series(1,3) x", "INSERT INTO tx_log VALUES(r.v);COMMIT;RAISE EXCEPTION 'loop failure';")+"$$", "CREATE PROCEDURE tx_cleanup_pause(IN seconds double precision) LANGUAGE plpgsql AS $$"+txLoopSource(bound, "SELECT x AS v FROM generate_series(1,3) x", "INSERT INTO tx_log VALUES(r.v);COMMIT;INSERT INTO tx_log VALUES(9);PERFORM pg_sleep(seconds);EXIT;")+"$$")
				want := proceduresDiagnostic("P0001", "loop failure", 0)
				want.Context = "PL/pgSQL function tx_cleanup_error() line 1 at RAISE"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CALL tx_cleanup_error()", want)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					txControlRows(t, p, b, c, 1)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 1))
					q := "CALL tx_cleanup_pause($1::double precision)"
					func() {
						var stmt *sql.Stmt
						if prepared {
							var err error
							stmt, err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT pg_sleep(seconds)\"\nPL/pgSQL function tx_cleanup_pause(double precision) line 1 at PERFORM", nil, func(ctx context.Context) fixture.RelationOutcome {
							var rows *sql.Rows
							var err error
							if stmt != nil {
								rows, err = stmt.QueryContext(ctx, 600)
							} else {
								rows, err = c.QueryContext(ctx, q, 600)
							}
							return fixture.RelationOutcome{Rows: rows, Err: err}
						})
						txControlRows(t, p, b, c, 1)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 1))
						target := fixture.Executor(c)
						if stmt != nil {
							target = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, target, b.Recorder, fixture.Exec(q, 0, 0))
						txControlRows(t, p, b, c, 1, 9)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 2))
					}()
					resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='tx_bound'", 0)
				}
			})
		})
	}
}
