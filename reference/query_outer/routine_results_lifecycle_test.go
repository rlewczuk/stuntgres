package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineResultsLifecycle(t *testing.T) {
	t.Run("dml_returning_completion", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsSetSetup(t, p, b, c)
			resultsExec(t, p, b, c,
				"CREATE TABLE rr_return(a integer,b integer)",
				"CREATE FUNCTION rr_insert_set(IN n integer,OUT a integer,OUT b integer) RETURNS SETOF record LANGUAGE SQL AS 'INSERT INTO rr_return VALUES($1,$1+1),($1,$1+1) RETURNING a,b'",
				"CREATE FUNCTION rr_insert_scalar(integer) RETURNS rr_pair LANGUAGE SQL AS 'INSERT INTO rr_return VALUES($1,$1+1),($1,$1+1) RETURNING a,b'")
			resultsBoth(t, p, b, c, "SELECT * FROM rr_insert_set($1::integer)", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(5)}, {int64(4), int64(5)}}, 4)
			resultsBoth(t, p, b, c, "SELECT rr_insert_scalar($1::integer) AS v", []string{"v"}, []string{""}, [][]any{{"(8,9)"}}, 8)
			resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM rr_return", 8)
			resultsStoredBoth(t, p, b, c, "SELECT a,b FROM rr_return ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(5)}, {int64(4), int64(5)}, {int64(4), int64(5)}, {int64(4), int64(5)}, {int64(8), int64(9)}, {int64(8), int64(9)}, {int64(8), int64(9)}, {int64(8), int64(9)}})
		})
	})
	t.Run("held_errors_and_root_atomicity", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsSetSetup(t, p, b, c)
			resultsExec(t, p, b, c,
				"CREATE TABLE rr_effect(v integer)",
				"CREATE TABLE rr_sink(a integer,b integer)",
				"CREATE FUNCTION rr_div(IN n integer,OUT a integer,OUT b integer) RETURNS SETOF record LANGUAGE SQL AS 'INSERT INTO rr_effect VALUES($1); SELECT k,10/(k-$1) FROM rr_rows ORDER BY k,v'")
			q := "INSERT INTO rr_sink SELECT * FROM rr_div($1::integer)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 3, 0))
			want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
			want.Context = `SQL function "rr_div" statement 2`
			resultsError(t, p, c, q, false, "execute", want, 2)
			result, err := stmt.ExecContext(p.Ctx, 2)
			if result != nil {
				t.Fatal("failed held set INSERT exposed result")
			}
			fixture.AssertDiagnostic(t, err, &want)
			resultsStoredBoth(t, p, b, c, "SELECT v FROM rr_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}})
			resultsStoredBoth(t, p, b, c, "SELECT a,b FROM rr_sink ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(5)}, {int64(2), int64(5)}})
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 3, 0))
			// A later outer expression error must roll back the callee INSERT.
			want = fixture.ControlsReferenceDiagnostic("22012", "division by zero")
			resultsError(t, p, c, "INSERT INTO rr_sink SELECT a,100/($1::integer-b) FROM rr_div(0)", false, "execute", want, 5)
			resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM rr_effect", 2)
			resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM rr_sink", 6)
			// Consume one row and explicitly close: later calls own fresh set state.
			rows, err := c.QueryContext(p.Ctx, "SELECT * FROM rr_table(2)")
			if err != nil {
				t.Fatal(err)
			}
			resultsMetadata(b, []string{"key", "val"}, []string{"INT4", "INT4"}, false)(t, rows)
			if !rows.Next() {
				t.Fatal("partial set missing first row", rows.Err())
			}
			var k, v int64
			if err := rows.Scan(&k, &v); err != nil || k != 1 || v != 10 {
				t.Fatal(k, v, err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			resultsSetCheck(t, p, b, c)
		})
	})
	t.Run("empty_quoted_revalidation", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE TABLE rr_live(a integer,b integer)",
				"CREATE TABLE rr_effect(v integer)",
				"CREATE FUNCTION rr_live_fn(IN n integer,OUT a integer,OUT b integer) RETURNS SETOF record LANGUAGE SQL AS 'INSERT INTO rr_effect VALUES($1); SELECT a,b FROM rr_live WHERE a<0'")
			resultsBoth(t, p, b, c, "SELECT * FROM rr_live_fn(4)", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{})
			resultsExec(t, p, b, c, "ALTER TABLE rr_live ALTER COLUMN b TYPE text USING b::text")
			want := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return record")
			want.Detail = "Final statement returns text instead of integer at column 2."
			want.Context = `SQL function "rr_live_fn" statement 2`
			resultsError(t, p, c, "SELECT * FROM rr_live_fn($1::integer)", true, "execute", want, 9)
			resultsStoredBoth(t, p, b, c, "SELECT v FROM rr_effect", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(4)}})
			resultsExec(t, p, b, c, "ALTER TABLE rr_live ALTER COLUMN b TYPE integer USING b::integer")
			resultsBoth(t, p, b, c, "SELECT * FROM rr_live_fn(4)", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{})
		})
	})
	t.Run("admitted_cancel_and_recovery", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE TABLE rr_effect(v integer)",
				"CREATE TABLE rr_sink(a integer,b integer)",
				"CREATE FUNCTION rr_pause(IN seconds double precision,OUT a integer,OUT b integer) RETURNS SETOF record LANGUAGE SQL AS 'INSERT INTO rr_effect VALUES(1); SELECT 5,8 FROM pg_sleep($1)'")
			q := "INSERT INTO rr_sink SELECT * FROM rr_pause($1::double precision)"
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
					fixture.SleepActiveCancelWithContext(t, p, b, c, q, `SQL function "rr_pause" statement 2`, nil, func(ctx context.Context) fixture.RelationOutcome {
						var result sql.Result
						var err error
						if stmt != nil {
							result, err = stmt.ExecContext(ctx, 600)
						} else {
							result, err = c.ExecContext(ctx, q, 600)
						}
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					if !b.PG {
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						result, err := c.ExecContext(ctx, q, 0)
						if result != nil || !errors.Is(err, context.Canceled) {
							t.Fatal("pre-canceled set operation", result, err)
						}
					}
					resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM rr_effect", 0)
					resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM rr_sink", 0)
					if stmt != nil {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, 0))
					} else {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 1, 0))
					}
					resultsStoredBoth(t, p, b, c, "SELECT a,b FROM rr_sink", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}})
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM rr_effect", 1))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM rr_sink", 1))
				}()
			}
		})
	})
}
