package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreFormalNamespace(t *testing.T) {
	for _, tc := range []struct{ name, parameters, duplicate string }{
		{"in_out", "IN x integer, OUT x integer", "x"},
		// Released add_parameter_name installs both positional and quoted names in
		// the same namespace; quoting does not exempt a positional-name collision.
		{"own_positional", `IN "$1" integer, OUT result integer`, "$1"},
		{"later_positional", `IN "$2" integer, OUT result integer`, "$2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				query := "CREATE FUNCTION pl_ns_conflict(" + tc.parameters + ") LANGUAGE plpgsql AS $$BEGIN $2 := $1+1; RETURN; END$$"
				want := fixture.ControlsReferenceDiagnostic("42P13", fmt.Sprintf("parameter name %q used more than once", tc.duplicate))
				want.Context = `compilation of PL/pgSQL function "pl_ns_conflict" near line 1`
				plpgsqlError(t, p, c, query, false, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='pl_ns_conflict'", "INT8", int64(0))
				// The corrected declaration has the same function name/input identity and
				// must work on this owner after both direct and prepared CREATE failures.
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_ns_conflict(IN input_value integer, OUT output_value integer) LANGUAGE plpgsql AS $$BEGIN $2 := $1+1; RETURN; END$$")
				plpgsqlValue(t, p, b, c, "SELECT pl_ns_conflict(input_value=>4) AS v", "INT4", int64(5))
				parametersHeld(t, p, b, c, "SELECT pl_ns_conflict($1) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
			})
		})
	}
	t.Run("sql_projection_policy", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION sql_ns_same(IN x integer, OUT x integer) LANGUAGE SQL AS 'SELECT $1+1'")
			plpgsqlValue(t, p, b, c, "SELECT sql_ns_same(x=>4) AS v", "INT4", int64(5))
			parametersHeld(t, p, b, c, "SELECT sql_ns_same($1) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
		})
	})
}
