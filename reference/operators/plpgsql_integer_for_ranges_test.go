package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Exact overload selection witnesses the private counter's SQL type without
// introducing an unrelated catalog-introspection builtin.
func integerForTypeWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE FUNCTION if_type_name(integer) RETURNS text LANGUAGE SQL AS 'SELECT ''integer''::text'",
		"CREATE FUNCTION if_type_name(text) RETURNS text LANGUAGE SQL AS 'SELECT ''text''::text'")
	plpgsqlValue(t, p, b, c, "SELECT if_type_name(1::integer) AS v", "TEXT", "integer")
	plpgsqlValue(t, p, b, c, "SELECT if_type_name('outer'::text) AS v", "TEXT", "text")
}

func TestReferencePLpgSQLIntegerForRanges(t *testing.T) {
	for _, tc := range []struct{ name, bounds, want string }{
		{"ascending", "1..5", "1,2,3,4,5,"},
		{"explicit_one", "1..5 BY 1", "1,2,3,4,5,"},
		{"reverse", "REVERSE 5..1", "5,4,3,2,1,"},
		{"step_endpoint", "1..10 BY 3", "1,4,7,10,"},
		{"step_past_endpoint", "1..11 BY 3", "1,4,7,10,"},
		{"reverse_step", "REVERSE 10..0 BY 3", "10,7,4,1,"},
		{"equal", "7..7", "7,"},
		{"reverse_equal", "REVERSE 7..7 BY 2147483647", "7,"},
		{"huge_step", "1..3 BY 2147483647", "1,"},
		{"forward_empty", "5..1", ""},
		{"reverse_empty", "REVERSE 1..5", ""},
		{"upper_limit", "2147483646..2147483647", "2147483646,2147483647,"},
		{"lower_limit", "REVERSE -2147483647..-2147483648", "-2147483647,-2147483648,"},
		{"equal_min", "-2147483648..-2147483648", "-2147483648,"},
		{"equal_max", "2147483647..2147483647", "2147483647,"},
		{"overshoot", "2147483620..2147483647 BY 10", "2147483620,2147483630,2147483640,"},
		{"reverse_overshoot", "REVERSE -2147483620..-2147483648 BY 10", "-2147483620,-2147483630,-2147483640,"},
		{"full_span", "-2147483648..2147483647 BY 2147483647", "-2147483648,-1,2147483646,"},
		{"reverse_full_span", "REVERSE 2147483647..-2147483648 BY 2147483647", "2147483647,0,-2147483647,"},
		{"parenthesized", "(1+0)..(2+1) BY (2-1)", "1,2,3,"},
		{"comments", "1 /* lower */ .. /* upper */ 3 BY /* step */ 1", "1,2,3,"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION if_range() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text:=''; BEGIN FOR i IN "+tc.bounds+" LOOP s:=s||i::text||','; END LOOP; RETURN s; END$$")
				plpgsqlValue(t, p, b, c, "SELECT if_range() AS v", "TEXT", tc.want)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DO $$BEGIN FOR i IN "+tc.bounds+" LOOP END LOOP; END$$", 0))
			})
		})
	}
	t.Run("aggregate", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION if_sum() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; s integer:=0; BEGIN FOR i IN 1..5 LOOP n:=n+1; s:=s+i; END LOOP; RETURN n*100+s; END$$")
			plpgsqlValue(t, p, b, c, "SELECT if_sum() AS v", "INT4", int64(515))
		})
	})
}
