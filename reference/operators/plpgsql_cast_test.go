package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func plpgsqlSelectedCast(t *testing.T) {
	t.Helper()
	t.Run("selected_cast_error", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE pl_cast_source AS ENUM ('42')",
				"CREATE FUNCTION pl_cast_fail(pl_cast_source) RETURNS integer LANGUAGE SQL AS 'SELECT 1/0'",
				"CREATE CAST (pl_cast_source AS integer) WITH FUNCTION pl_cast_fail(pl_cast_source) AS ASSIGNMENT",
				"CREATE FUNCTION pl_cast_target() RETURNS integer LANGUAGE plpgsql AS $$DECLARE s pl_cast_source := '42'; n integer; BEGIN n := s; RETURN n; END$$")
			// IO would successfully produce42. A selected cast's own22012 must
			// propagate; catching it and trying IO would mask a real SQL error.
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "pl_cast_target()", "assignment")
			// Native SQL execution retains its statement frame rather than
			// claiming PG's optimizer phase (the incumbent SQL-routine boundary).
			sqlContext := `SQL function "pl_cast_fail" statement 1`
			if b.PG {
				sqlContext = `SQL function "pl_cast_fail" during inlining`
			}
			want.Context = sqlContext + "\nPL/pgSQL assignment \"n := s\"\n" + want.Context
			plpgsqlError(t, p, c, "SELECT pl_cast_target()", true, want)
			plpgsqlValue(t, p, b, c, "SELECT '42'::pl_cast_source::text AS v", "TEXT", "42")
		})
	})
}
