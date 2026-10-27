package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLDynamicSQLLifecycle(t *testing.T) {
	t.Run("original_held_and_native_replay", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE ds_life_marks(i integer)",
				"CREATE FUNCTION ds_life(command text,n integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN EXECUTE command INTO x USING n; INSERT INTO ds_life_marks VALUES(x); RETURN x; END$$",
				"CREATE FUNCTION ds_shape(command text) RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; BEGIN EXECUTE command INTO r; RETURN r::text; END$$")
			q := "SELECT ds_life($1::text,$2::integer) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ds_life_marks", "INT8", int64(0))
			check := func() {
				for _, tc := range []struct {
					command string
					n       int
					want    int64
				}{
					{"SELECT $1::integer+1", 4, 5}, {"SELECT $1::integer+2", 8, 10}, {"SELECT $1::integer", 12, 12},
				} {
					s := sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.command, tc.n)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				for _, tc := range []struct {
					command, code, message string
					position               int
				}{
					{"SELEC 1", "42601", "syntax error at or near \"SELEC\"", 1},
					{"SELECT missing_dynamic", "42703", "column \"missing_dynamic\" does not exist", 8},
					{"SELECT 1/$1::integer", "22012", "division by zero", 0},
				} {
					d := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "ds_life(text,integer)", "EXECUTE")
					if tc.position > 0 {
						d.InternalQuery = tc.command
						d.InternalPosition = fixture.Position(tc.position)
					} else {
						d.Context = "SQL statement \"" + tc.command + "\"\n" + d.Context
					}
					s := fixture.ErrorDiagnostic(q, d, tc.command, 0)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, "SELECT $1::integer+1", 4))
				}
				plpgsqlValue(t, p, b, c, "SELECT ds_shape('SELECT 1::integer AS a') AS v", "TEXT", "(1)")
				plpgsqlValue(t, p, b, c, "SELECT ds_shape('SELECT ''text''::text AS b,2::integer AS c') AS v", "TEXT", "(text,2)")
				userOperatorExec(t, p, b, c, "CREATE TABLE ds_replanned(v integer)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ds_replanned VALUES(31)", 1))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(31)}}, "SELECT v FROM ds_replanned", 0))
				userOperatorExec(t, p, b, c, "DROP TABLE ds_replanned", "CREATE TABLE ds_replanned(v text)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ds_replanned VALUES('42')", 1))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}, "SELECT v::integer FROM ds_replanned", 0))
				userOperatorExec(t, p, b, c, "DROP TABLE ds_replanned")
			}
			check()
			for range 2 {
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				}
				check()
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct_cancel"
		if prepared {
			name = "held_cancel"
		}
		t.Run(name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE ds_cancel_marks(i integer)",
					"CREATE FUNCTION ds_cancel(slow boolean) RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO ds_cancel_marks VALUES(9); IF slow THEN EXECUTE 'SELECT pg_sleep(30)'; END IF; RETURN 5; END$$")
				q := "SELECT ds_cancel($1::boolean) AS v"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT pg_sleep(30)\"\nPL/pgSQL function ds_cancel(boolean) line 1 at EXECUTE", nil, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if stmt != nil {
						rows, err = stmt.QueryContext(ctx, true)
					} else {
						rows, err = c.QueryContext(ctx, q, true)
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ds_cancel_marks", "INT8", int64(0))
				var executor fixture.Executor = c
				if stmt != nil {
					executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, false))
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ds_cancel_marks", "INT8", int64(1))
			})
		})
	}
}
