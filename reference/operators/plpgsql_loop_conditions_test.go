package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func loopReachedConditions(t *testing.T) {
	for _, kind := range []string{"EXIT", "CONTINUE"} {
		t.Run(kind, func(t *testing.T) {
			loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION loop_lazy(x boolean) RETURNS integer LANGUAGE plpgsql AS $$BEGIN LOOP IF x THEN "+kind+" WHEN loop_missing(); END IF; EXIT; END LOOP; RETURN 7; END$$")
				q := "SELECT loop_lazy($1) AS v"
				parametersValue(t, p, b, c, q, "v", "INT4", int64(7), false)
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				want := plpgsqlRuntimeDiagnostic("42883", "function loop_missing() does not exist", "loop_lazy(boolean)", kind)
				want.Hint = parametersNoMatchHint
				want.InternalPosition = "1"
				want.InternalQuery = "loop_missing()"
				for _, input := range []bool{false, true, false} {
					if !input {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, input))
						continue
					}
					rows, err := stmt.QueryContext(p.Ctx, input)
					if rows != nil {
						rows.Close()
						t.Fatal("missing condition function exposed rows")
					}
					if err == nil {
						t.Fatal("missing condition function succeeded")
					}
					fixture.AssertDiagnostic(t, err, &want)
				}
				plpgsqlError(t, p, c, "SELECT loop_lazy(true) AS v", true, want)
			})
		})
		t.Run(kind+"_selected_cast", func(t *testing.T) {
			loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Text 'yes' could succeed via IO. Once the assignment cast is
				// selected, its error must not fall back to successful text IO.
				userOperatorExec(t, p, b, c,
					"CREATE TYPE loop_bool_source AS ENUM ('yes')",
					"CREATE FUNCTION loop_bool_fail(loop_bool_source) RETURNS boolean LANGUAGE SQL AS 'SELECT 1/0=0'",
					"CREATE CAST (loop_bool_source AS boolean) WITH FUNCTION loop_bool_fail(loop_bool_source) AS ASSIGNMENT",
					"CREATE FUNCTION loop_cast() RETURNS integer LANGUAGE plpgsql AS $$BEGIN LOOP "+kind+" WHEN 'yes'::loop_bool_source; EXIT; END LOOP; RETURN 7; END$$")
				want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "loop_cast()", kind)
				want.Context = "SQL function \"loop_bool_fail\" statement 1\n" + want.Context
				plpgsqlError(t, p, c, "SELECT loop_cast() AS v", true, want)
				plpgsqlValue(t, p, b, c, "SELECT 'yes'::loop_bool_source::text AS v", "TEXT", "yes")
			})
		})
	}
}
