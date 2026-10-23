package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func plpgsqlCompositeAssignmentErrors(t *testing.T) {
	t.Run("malformed_field_input", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE pa_to AS (n integer)",
				"CREATE FUNCTION pa_composite_error() RETURNS integer LANGUAGE plpgsql AS $$DECLARE a pa_to[] := ARRAY[NULL::pa_to]; BEGIN a[1] := '(bad)'::text; RETURN 1; END$$")
			want := plpgsqlRuntimeDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, "pa_composite_error()", "assignment")
			plpgsqlError(t, p, c, "SELECT pa_composite_error()", true, want)
		})
	})
}
