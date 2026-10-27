package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func txLoopSource(bound bool, query, body string) string {
	if bound {
		return "DECLARE tx_bound CURSOR FOR " + query + ";BEGIN FOR r IN tx_bound LOOP " + body + " END LOOP;END"
	}
	return "DECLARE r record;BEGIN FOR r IN " + query + " LOOP " + body + " END LOOP;END"
}

func TestReferencePLpgSQLTxControlCursorHolding(t *testing.T) {
	txControlNestedHolding(t)
	for _, bound := range []bool{false, true} {
		for _, command := range []string{"COMMIT", "ROLLBACK"} {
			name := "implicit"
			if bound {
				name = "bound"
			}
			t.Run(name+"/"+command, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "CREATE TABLE tx_source(v integer)", "CREATE SEQUENCE tx_eval START 1", "CREATE PROCEDURE tx_hold(OUT visits text) LANGUAGE plpgsql AS $$"+txLoopSource(bound, "SELECT v,nextval('tx_eval') AS serial FROM tx_source ORDER BY v", "visits:=coalesce(visits,'')||r.v::text;"+command+";IF r.v=1 THEN DELETE FROM tx_source;INSERT INTO tx_source VALUES(4);END IF;")+"$$")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_source VALUES(1),(2),(3)", 3))
					for _, prepared := range []bool{false, true} {
						s := proceduresStep(b, "CALL tx_hold(NULL)", []string{"visits"}, []string{"TEXT"}, [][]any{{"123"}})
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						values := [][]any{{int64(4)}}
						if command == "ROLLBACK" {
							values = [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
						}
						resultsStoredBoth(t, p, b, c, "SELECT v FROM tx_source ORDER BY v", []string{"v"}, []string{"INT4"}, values)
						resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='tx_bound'", 0)
						// The default scrollable bound cursor rewinds for hold:
						// its already fetched first row is evaluated again. The
						// implicit loop instead holds only its remaining source.
						evaluations := int64(3)
						if bound {
							evaluations = 4
						}
						resultsBothMetadata(t, p, b, c, "SELECT last_value FROM tx_eval", []string{"last_value"}, []string{"INT8"}, [][]any{{evaluations}}, []resultsNativeColumn{resultsNonNullMetadata})
						resultsExec(t, p, b, c, "ALTER SEQUENCE tx_eval RESTART WITH 1")
						count := int64(1)
						if command == "ROLLBACK" {
							count = 3
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_source", count))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_source VALUES(1),(2),(3)", 3))
					}
				})
			})
		}
	}
	for _, bound := range []bool{false, true} {
		name := "implicit"
		if bound {
			name = "bound"
		}
		t.Run(name+"/locks", func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE tx_source(v integer)", "CREATE PROCEDURE tx_lock_pause(IN seconds double precision) LANGUAGE plpgsql AS $$"+txLoopSource(bound, "SELECT v FROM tx_source ORDER BY v FOR UPDATE", "COMMIT;IF r.v=1 THEN PERFORM pg_sleep(seconds);END IF;")+"$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_source VALUES(1),(2),(3)", 3))
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				for _, prepared := range []bool{false, true} {
					func() {
						q := "CALL tx_lock_pause($1::double precision)"
						var stmt *sql.Stmt
						if prepared {
							var err error
							stmt, err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT pg_sleep(seconds)\"\nPL/pgSQL function tx_lock_pause(double precision) line 1 at PERFORM", func(ctx context.Context) {
							tx, err := peer.BeginTx(ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							fixture.RunStep(t, ctx, tx, b.Recorder, fixture.Select("SELECT v FROM tx_source ORDER BY v FOR UPDATE NOWAIT", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
							fixture.RunStep(t, ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE tx_source IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
						}, func(ctx context.Context) fixture.RelationOutcome {
							var rows *sql.Rows
							var err error
							if stmt != nil {
								rows, err = stmt.QueryContext(ctx, 600)
							} else {
								rows, err = c.QueryContext(ctx, q, 600)
							}
							return fixture.RelationOutcome{Rows: rows, Err: err}
						})
						resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='tx_bound'", 0)
						target := fixture.Executor(c)
						if stmt != nil {
							target = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, target, b.Recorder, fixture.Exec(q, 0, 0))
					}()
				}
			})
		})
	}
	for _, command := range []string{"COMMIT", "ROLLBACK"} {
		t.Run("open/"+command, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, operation := range []string{"FETCH tx_open INTO x", "CLOSE tx_open"} {
					resultsExec(t, p, b, c, "CREATE OR REPLACE PROCEDURE tx_open_end() LANGUAGE plpgsql AS $$DECLARE tx_open refcursor:='tx_open';x integer;BEGIN OPEN tx_open FOR SELECT 1;"+command+";"+operation+";END$$")
					want := proceduresDiagnostic("34000", `cursor "tx_open" does not exist`, 0)
					action := "FETCH"
					if operation == "CLOSE tx_open" {
						action = "CLOSE"
					}
					want.Context = "PL/pgSQL function tx_open_end() line 1 at " + action
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("CALL tx_open_end()", want)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
		})
	}
}

func TestReferencePLpgSQLTxControlCursorErrors(t *testing.T) {
	for _, command := range []string{"COMMIT", "ROLLBACK"} {
		txControlEnclosingWritable(t, command)
		for _, query := range []string{"UPDATE tx_log SET v=v+1 RETURNING v", "INSERT INTO tx_log VALUES(2) RETURNING v", "DELETE FROM tx_log RETURNING v", "WITH changed AS (INSERT INTO tx_log VALUES(2) RETURNING v) SELECT v FROM changed"} {
			t.Run(command+"/"+query, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_writable() LANGUAGE plpgsql AS $$"+txLoopSource(false, query, command+";")+"$$")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_log VALUES(1)", 1))
					want := proceduresDiagnostic("55000", "cannot perform transaction commands inside a cursor loop that is not read-only", 0)
					want.Context = "PL/pgSQL function tx_writable() line 1 at " + command
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("CALL tx_writable()", want)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						txControlRows(t, p, b, c, 1)
					}
				})
			})
		}
		for _, bound := range []bool{false, true} {
			name := "implicit"
			if bound {
				name = "bound"
			}
			t.Run(command+"/late/"+name, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_late() LANGUAGE plpgsql AS $$"+txLoopSource(bound, "SELECT x,1/(x-1000) AS answer FROM generate_series(1,1000) x", "INSERT INTO tx_log VALUES(r.x);"+command+";")+"$$")
					want := proceduresDiagnostic("22012", "division by zero", 0)
					want.Context = "PL/pgSQL function tx_late() line 1 at " + command
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("CALL tx_late()", want)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						txControlRows(t, p, b, c)
					}
				})
			})
		}
	}
}

func TestReferencePLpgSQLTxControlCursorCleanup(t *testing.T) {
	txControlCursorErrorCancel(t)
	for _, bound := range []bool{false, true} {
		for _, transfer := range []string{"EXIT", "RETURN", "CONTINUE", "EXIT tx_outer"} {
			name := "implicit"
			if bound {
				name = "bound"
			}
			t.Run(name+"/"+transfer, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					source := txLoopSource(bound, "SELECT x AS v FROM generate_series(1,3) x", "INSERT INTO tx_log VALUES(r.v);COMMIT;"+transfer+";")
					if transfer == "EXIT tx_outer" {
						source = "<<tx_outer>> " + source
					}
					resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_cleanup() LANGUAGE plpgsql AS $$"+source+"$$")
					for _, prepared := range []bool{false, true} {
						s := fixture.Exec("CALL tx_cleanup()", 0)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						if transfer == "CONTINUE" {
							txControlRows(t, p, b, c, 1, 2, 3)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 3))
						} else {
							txControlRows(t, p, b, c, 1)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 1))
						}
						resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='tx_bound'", 0)
					}
				})
			})
		}
	}
}
