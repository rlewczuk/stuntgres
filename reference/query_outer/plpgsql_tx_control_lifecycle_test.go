package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePLpgSQLTxControlVisibilityCancellation(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_pause(IN seconds double precision) LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(1);COMMIT;INSERT INTO tx_log VALUES(2);PERFORM pg_sleep(seconds);INSERT INTO tx_log VALUES(3);END$$")
		observer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		q := "CALL tx_pause($1::double precision)"
		for _, prepared := range []bool{false, true} {
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
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT pg_sleep(seconds)\"\nPL/pgSQL function tx_pause(double precision) line 1 at PERFORM", func(ctx context.Context) {
					fixture.RunStep(t, ctx, observer, b.Recorder, fixture.Select("SELECT v FROM tx_log ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
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
				txControlRows(t, p, b, observer, 1)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 1))
				target := fixture.Executor(c)
				if stmt != nil {
					target = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				fixture.RunStep(t, p.Ctx, target, b.Recorder, fixture.Exec(q, 0, 0))
				txControlRows(t, p, b, observer, 1, 2, 3)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 3))
			}()
		}
		if !b.PG {
			for _, expired := range []bool{false, true} {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				want := error(context.Canceled)
				if expired {
					ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
					want = context.DeadlineExceeded
				}
				_, err := c.ExecContext(ctx, q, 0)
				cancel()
				if !errors.Is(err, want) {
					t.Fatal(err, want)
				}
				txControlRows(t, p, b, c)
			}
		}
		for _, command := range []string{"COMMIT", "ROLLBACK"} {
			resultsExec(t, p, b, c, "CREATE OR REPLACE PROCEDURE tx_fail() LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(1);"+command+";INSERT INTO tx_log VALUES(2);PERFORM 1/0;END$$")
			want := proceduresDiagnostic("22012", "division by zero", 0)
			want.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function tx_fail() line 1 at PERFORM"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CALL tx_fail()", want)
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				if command == "COMMIT" {
					txControlRows(t, p, b, c, 1)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 1))
				} else {
					txControlRows(t, p, b, c)
				}
			}
		}
	})
}

func TestReferencePLpgSQLTxControlPreparedReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			txControlSetup(t, p, b, c)
			resultsExec(t, p, b, c, "CREATE PROCEDURE tx_reuse(IN n integer,INOUT x integer) LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(n);COMMIT AND CHAIN;x:=x+1;IF n<0 THEN PERFORM 1/0;END IF;INSERT INTO tx_log VALUES(n+2);END$$")
			q := "CALL tx_reuse($1::integer,$2::integer)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			txControlRows(t, p, b, c)
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"x"}, []string{"INT4"}, [][]any{{int64(11)}}, 1, 10))
			want := proceduresDiagnostic("22012", "division by zero", 0)
			want.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function tx_reuse(integer,integer) line 1 at PERFORM"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, want, -1, 10))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"x"}, []string{"INT4"}, [][]any{{int64(21)}}, 5, 20))
			txControlRows(t, p, b, c, -1, 1, 3, 5, 7)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 5))
			resultsExec(t, p, b, c, "CREATE PROCEDURE tx_replay_hold(OUT visits text) LANGUAGE plpgsql AS $$"+txLoopSource(true, "SELECT x AS v FROM generate_series(1,3) x", "visits:=coalesce(visits,'')||r.v::text;IF r.v=1 THEN COMMIT;ELSE ROLLBACK;END IF;")+"$$")
			check := func(conn *sql.Conn, backend fixture.Backend) {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CALL tx_segments(0)", 0))
				txControlRows(t, p, backend, conn, 1, 3)
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("DELETE FROM tx_log", 2))
				proceduresBoth(t, p, backend, conn, "CALL tx_reuse(10,20)", []string{"x"}, []string{"INT4"}, [][]any{{int64(21)}})
				txControlRows(t, p, backend, conn, 10, 10, 12, 12)
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("DELETE FROM tx_log", 4))
				proceduresBoth(t, p, backend, conn, "CALL tx_replay_hold(NULL)", []string{"visits"}, []string{"TEXT"}, [][]any{{"123"}})
				resultsCountBoth(t, p, backend, conn, "SELECT count(*) AS v FROM pg_cursors WHERE name='tx_bound'", 0)
			}
			check(c, b)
			if b.PG {
				return
			}
			var emitted []byte
			for range 2 {
				emitted = resultsNativeReplayRound(t, p, b, c, check)
				if p.Postgres != nil {
					func() {
						pg, err := p.Postgres.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer pg.Close()
						resultsExec(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg, "DROP SCHEMA public CASCADE;CREATE SCHEMA public;GRANT USAGE ON SCHEMA public TO PUBLIC")
						if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
							t.Fatal(err)
						}
						if _, err := pg.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION;SET check_function_bodies=true"); err != nil {
							t.Fatal(err)
						}
						check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
						if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
					}()
				}
			}
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
			if err != nil {
				t.Fatal(err)
			}
			raw := sql.OpenDB(connector)
			defer raw.Close()
			recorded, rec := fixture.RecordedNativePool(t, connector)
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			clone, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer clone.Close()
			check(clone, fixture.Backend{Recorder: rec})
		})
	}
}
