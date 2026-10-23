package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLAssignmentBounds(t *testing.T) {
	for _, tc := range []struct{ name, initial, target, rhs, want string }{
		{"null_element", "NULL", "[3]", "4", "[3:3]={4}"},
		{"null_rank_two", "NULL", "[1][4]", "4", "[1:1][4:4]={{4}}"},
		{"extend_upper", "'{1,2,3}'", "[5]", "9", "{1,2,3,NULL,9}"},
		{"extend_lower", "'{1,2,3}'", "[-1]", "9", "[-1:3]={9,NULL,1,2,3}"},
		{"slice_extend", "'{1,2}'", "[2:3]", "'{3,4}'::integer[]", "{1,3,4}"},
		{"extra_source", "'{1,2}'", "[1:2]", "'{7,8,9}'::integer[]", "{7,8}"},
		{"null_slice", "'{1,2}'", "[1:2]", "NULL::integer[]", "{1,2}"},
		{"null_target_null_slice", "NULL", "[1:2]", "NULL::integer[]", "{}"},
		{"empty_target", "'{}'", "[3:4]", "'{7,8}'::integer[]", "[3:4]={7,8}"},
		{"non_one", "'[0:2]={1,2,3}'", "[0:1]", "'{7,8}'::integer[]", "[0:2]={7,8,3}"},
		{"omitted_lower", "'{1,2,3}'", "[:2]", "'{7,8}'::integer[]", "{7,8,3}"},
		{"omitted_upper", "'{1,2,3}'", "[2:]", "'{7,8}'::integer[]", "{1,7,8}"},
		{"omitted_both", "'{1,2,3}'", "[:]", "'{7,8,9}'::integer[]", "{7,8,9}"},
		{"rank_two_element", "'{{1,2},{3,4}}'", "[2][1]", "7", "{{1,2},{7,4}}"},
		{"rank_two_slice", "'{{1,2},{3,4}}'", "[1:2][2:2]", "'{{7},{8}}'::integer[]", "{{1,7},{3,8}}"},
		{"rank_two_omitted", "'{{1,2},{3,4}}'", "[:][2:]", "'{{7},{8}}'::integer[]", "{{1,7},{3,8}}"},
		{"rank_two_non_one", "'[0:1][3:4]={{1,2},{3,4}}'", "[0:1][4:4]", "'{{7},{8}}'::integer[]", "[0:1][3:4]={{1,7},{3,8}}"},
		// A colon in any dimension makes every dimension a slice. An
		// element-looking dimension means 1:upper, not a single element.
		{"mixed_leading_element", "'{{1,2},{3,4}}'", "[2][2:2]", "'{{7},{8}}'::integer[]", "{{1,7},{3,8}}"},
		{"mixed_trailing_element", "'{{1,2},{3,4}}'", "[2:2][2]", "'{{7,8}}'::integer[]", "{{1,2},{7,8}}"},
		{"mixed_omitted_lower", "'{{1,2},{3,4}}'", "[2][:2]", "'{{7,8},{9,10}}'::integer[]", "{{7,8},{9,10}}"},
		{"mixed_omitted_upper", "'{{1,2},{3,4}}'", "[2:][2]", "'{{7,8}}'::integer[]", "{{1,2},{7,8}}"},
		{"mixed_omitted_both", "'{{1,2},{3,4}}'", "[:][2]", "'{{7,8},{9,10}}'::integer[]", "{{7,8},{9,10}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pa_bounds() RETURNS integer[] LANGUAGE plpgsql AS $$DECLARE a integer[] := "+tc.initial+"; BEGIN a"+tc.target+" := "+tc.rhs+"; RETURN a; END$$")
				plpgsqlValue(t, p, b, c, "SELECT pa_bounds()::text AS v", "TEXT", tc.want)
			})
		})
	}
}
