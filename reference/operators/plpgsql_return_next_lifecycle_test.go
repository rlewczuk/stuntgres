package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLReturnNextLifecycle(t *testing.T) {
	t.Run("original_held_and_replay", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE rn_life_row(id integer)", "CREATE TABLE rn_life_marks(id integer)",
				"CREATE FUNCTION rn_child() RETURNS SETOF rn_life_row LANGUAGE plpgsql AS $$DECLARE r rn_life_row; BEGIN SELECT 10 INTO r; RETURN NEXT r; r.id:=11; RETURN NEXT r; END$$",
				"CREATE FUNCTION rn_life(mode integer) RETURNS SETOF rn_life_row LANGUAGE plpgsql AS $$DECLARE r rn_life_row; saved_f boolean; saved_n bigint; n bigint; BEGIN IF mode=0 THEN RETURN; END IF; INSERT INTO rn_life_marks VALUES(1); SELECT 1 INTO r; saved_f:=FOUND; GET DIAGNOSTICS saved_n=ROW_COUNT; RETURN NEXT r; IF mode=2 THEN EXECUTE 'SELECT 1/0'; END IF; PERFORM * FROM rn_child(); SELECT 1 INTO r; saved_f:=FOUND; GET DIAGNOSTICS saved_n=ROW_COUNT; r.id:=2; RETURN NEXT r; GET DIAGNOSTICS n=ROW_COUNT; IF FOUND IS DISTINCT FROM saved_f OR n IS DISTINCT FROM saved_n THEN RAISE EXCEPTION 'status changed'; END IF; RETURN; END$$")
			q := "SELECT * FROM rn_life($1::integer) ORDER BY id"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			// Keep scanned public values after releasing the connection's cursor.
			retained, err := stmt.QueryContext(p.Ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			var saved []any
			for retained.Next() {
				var value any
				if err := retained.Scan(&value); err != nil {
					t.Fatal(err)
				}
				saved = append(saved, value)
			}
			if err := retained.Err(); err != nil {
				t.Fatal(err)
			}
			retained.Close()
			check := func() {
				for _, executor := range []fixture.Executor{c, held} {
					userOperatorExec(t, p, b, c, "TRUNCATE rn_life_marks")
					d := plpgsqlRuntimeDiagnostic("22012", "division by zero", "rn_life(integer)", "EXECUTE")
					d.Context = "SQL statement \"SELECT 1/0\"\n" + d.Context
					s := fixture.ErrorDiagnostic(q, d, 2)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					sqlExecutionCount(t, p, b, c, "rn_life_marks", 0)
					s = sqlExecutionStep(b, q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, 1)
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
					sqlExecutionCount(t, p, b, c, "rn_life_marks", 1)
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, sqlExecutionStep(b, q, []string{"id"}, []string{"INT4"}, nil, 0))
				}
				s := sqlExecutionStep(b, q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, 1)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
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
			if len(saved) != 2 || saved[0] != int64(1) || saved[1] != int64(2) {
				t.Fatalf("retained values=%v", saved)
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
				userOperatorExec(t, p, b, c, "CREATE TABLE rn_cancel_row(id integer)", "CREATE TABLE rn_cancel_marks(id integer)", "CREATE FUNCTION rn_cancel(slow boolean) RETURNS SETOF rn_cancel_row LANGUAGE plpgsql AS $$DECLARE r rn_cancel_row; BEGIN INSERT INTO rn_cancel_marks VALUES(9); SELECT 5 INTO r; RETURN NEXT r; IF slow THEN EXECUTE 'SELECT pg_sleep(30)'; END IF; END$$")
				q := "SELECT * FROM rn_cancel($1::boolean)"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				for round := range 3 {
					if round > 0 && !b.PG {
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
					userOperatorExec(t, p, b, c, "TRUNCATE rn_cancel_marks")
					fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \"SELECT pg_sleep(30)\"\nPL/pgSQL function rn_cancel(boolean) line 1 at EXECUTE", nil, func(ctx context.Context) fixture.RelationOutcome {
						var rows *sql.Rows
						var err error
						if stmt != nil {
							rows, err = stmt.QueryContext(ctx, true)
						} else {
							rows, err = c.QueryContext(ctx, q, true)
						}
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					sqlExecutionCount(t, p, b, c, "rn_cancel_marks", 0)
					var executor fixture.Executor = c
					if stmt != nil {
						executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, sqlExecutionStep(b, q, []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}, false))
					sqlExecutionCount(t, p, b, c, "rn_cancel_marks", 1)
				}
			})
		})
	}
}
