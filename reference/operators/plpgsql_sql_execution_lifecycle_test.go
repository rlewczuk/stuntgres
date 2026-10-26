package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSQLExecutionLifecycle(t *testing.T) {
	t.Run("direct_fresh_original_held", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE sx_life_marks(i integer)",
				"CREATE PROCEDURE sx_life_bump(INOUT x integer) LANGUAGE SQL AS 'SELECT x+1'",
				"CREATE FUNCTION sx_life(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_life_marks VALUES(x); PERFORM 1 WHERE false; CALL sx_life_bump(x); RETURN x; END$$",
				"CREATE PROCEDURE sx_life_outer(INOUT x integer) LANGUAGE plpgsql AS $$BEGIN CALL sx_life_bump(x); END$$",
				"CREATE PROCEDURE sx_life_noout(IN x integer) LANGUAGE plpgsql AS $$BEGIN PERFORM x; END$$")
			for _, tc := range []struct {
				q            string
				names, types []string
			}{
				{"SELECT sx_life($1::integer) AS v", []string{"v"}, []string{"INT4"}},
				{"CALL sx_life_outer($1::integer)", []string{"x"}, []string{"INT4"}},
			} {
				stmt, err := c.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer stmt.Close()
					sqlExecutionCount(t, p, b, c, "sx_life_marks", 0)
					for route := range 3 {
						for _, value := range []struct{ in, out any }{{4, int64(5)}, {8, int64(9)}, {nil, nil}, {4, int64(5)}} {
							s := sqlExecutionStep(b, tc.q, tc.names, tc.types, [][]any{{value.out}}, value.in)
							var executor fixture.Executor = c
							if route == 1 {
								s.Prepared = true
							}
							if route == 2 {
								executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
							}
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
							count := int64(0)
							if tc.names[0] == "v" {
								count = 1
							}
							sqlExecutionCount(t, p, b, c, "sx_life_marks", count)
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sx_life_marks", count))
						}
					}
				}()
			}
			sqlExecutionBoth(t, p, b, c, "CALL sx_life_noout(4)", []string{}, []string{}, [][]any{})
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("CALL sx_life_noout(4)", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			if !b.PG {
				q := "SELECT sx_life($1::integer) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, expired := range []bool{false, true} {
					ctx, cancel := context.WithCancel(p.Ctx)
					want := context.Canceled
					if expired {
						cancel()
						ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
						want = context.DeadlineExceeded
					} else {
						cancel()
					}
					for _, held := range []bool{false, true} {
						var rows *sql.Rows
						var err error
						if held {
							rows, err = stmt.QueryContext(ctx, 4)
						} else {
							rows, err = c.QueryContext(ctx, q, 4)
						}
						if rows != nil {
							rows.Close()
						}
						if !errors.Is(err, want) {
							t.Fatalf("context error=%v want=%v", err, want)
						}
					}
					<-ctx.Done()
					if !errors.Is(ctx.Err(), want) {
						t.Fatal(ctx.Err())
					}
					cancel()
					sqlExecutionCount(t, p, b, c, "sx_life_marks", 0)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, 4))
			}
		})
	})
	for _, mode := range []string{"perform", "sql_call", "pl_call"} {
		for _, held := range []bool{false, true} {
			name := mode + "_direct"
			if held {
				name = mode + "_held"
			}
			t.Run(name, func(t *testing.T) {
				sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE TABLE sx_cancel_marks(i integer)",
						"CREATE PROCEDURE sx_sleep_sql() LANGUAGE SQL AS 'SELECT pg_sleep(30)'",
						"CREATE PROCEDURE sx_sleep_pl() LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(30); END$$")
					command := "PERFORM pg_sleep(30)"
					wantContext := "SQL statement \"SELECT pg_sleep(30)\"\nPL/pgSQL function sx_cancel(boolean) line 1 at PERFORM"
					if mode == "sql_call" {
						command = "CALL sx_sleep_sql()"
						wantContext = "SQL function \"sx_sleep_sql\" statement 1\nSQL statement \"CALL sx_sleep_sql()\"\nPL/pgSQL function sx_cancel(boolean) line 1 at CALL"
					} else if mode == "pl_call" {
						command = "CALL sx_sleep_pl()"
						wantContext = "SQL statement \"SELECT pg_sleep(30)\"\nPL/pgSQL function sx_sleep_pl() line 1 at PERFORM\nSQL statement \"CALL sx_sleep_pl()\"\nPL/pgSQL function sx_cancel(boolean) line 1 at CALL"
					}
					userOperatorExec(t, p, b, c, "CREATE FUNCTION sx_cancel(slow boolean) RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_cancel_marks VALUES(9); IF slow THEN "+command+"; END IF; RETURN 5; END$$")
					q := "SELECT sx_cancel($1::boolean) AS v"
					var stmt *sql.Stmt
					if held {
						var err error
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					fixture.SleepActiveCancelWithContext(t, p, b, c, q, wantContext, nil, func(ctx context.Context) fixture.RelationOutcome {
						var rows *sql.Rows
						var err error
						if stmt != nil {
							rows, err = stmt.QueryContext(ctx, true)
						} else {
							rows, err = c.QueryContext(ctx, q, true)
						}
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					sqlExecutionCount(t, p, b, c, "sx_cancel_marks", 0)
					var executor fixture.Executor = c
					if stmt != nil {
						executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, false))
					sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM sx_cancel_marks", "INT4", int64(9))
					sqlExecutionValue(t, p, b, c, "SELECT 1 AS v", "INT4", int64(1))
				})
			})
		}
	}
}
