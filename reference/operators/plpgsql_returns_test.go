package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreReturns(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION pl_void() RETURNS void LANGUAGE plpgsql AS $$BEGIN RETURN; END$$",
			"CREATE FUNCTION pl_void_end() RETURNS void LANGUAGE plpgsql AS $$BEGIN END$$",
			"CREATE FUNCTION sql_void() RETURNS void LANGUAGE SQL AS 'SELECT 1'",
			"CREATE FUNCTION pl_null() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
			"CREATE FUNCTION pl_fallthrough() RETURNS integer LANGUAGE plpgsql AS $$BEGIN END$$")
		for _, f := range []string{"pl_void", "pl_void_end"} {
			plpgsqlValue(t, p, b, c, "SELECT "+f+"() IS NULL AS v", "BOOL", false)
			plpgsqlValue(t, p, b, c, "SELECT "+f+"()::text AS v", "TEXT", "")
			plpgsqlValue(t, p, b, c, "SELECT "+f+"() AS v", "VOID", "")
		}
		plpgsqlValue(t, p, b, c, "SELECT sql_void() IS NULL AS v", "BOOL", true)
		plpgsqlValue(t, p, b, c, "SELECT pl_null() AS v", "INT4", nil)
		want := fixture.ControlsReferenceDiagnostic("2F005", "control reached end of function without RETURN")
		want.Context = "PL/pgSQL function pl_fallthrough()"
		plpgsqlError(t, p, c, "SELECT pl_fallthrough()", true, want)
	})
	for _, tc := range []struct{ name, declaration, body, code, message, position string }{
		{"void_expression", "FUNCTION pl_bad_return() RETURNS void", "BEGIN RETURN 1; END", "42804", "RETURN cannot have a parameter in function returning void", "81"},
		{"scalar_bare", "FUNCTION pl_bad_return() RETURNS integer", "BEGIN RETURN; END", "42601", "missing expression at or near \";\"", "83"},
		{"out_expression", "FUNCTION pl_bad_return(OUT y integer)", "BEGIN RETURN 1; END", "42804", "RETURN cannot have a parameter in function with OUT parameters", "81"},
		{"procedure_expression", "PROCEDURE pl_bad_return()", "BEGIN RETURN 1; END", "42601", "RETURN cannot have a parameter in a procedure", "69"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = tc.position
				plpgsqlError(t, p, c, "CREATE "+tc.declaration+" LANGUAGE plpgsql AS $$"+tc.body+"$$", false, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='pl_bad_return'", "INT8", int64(0))
			})
		})
	}
}

func TestReferencePLpgSQLCoreLazyBinding(t *testing.T) {
	plpgsqlExpressionCases(t)
	for _, tc := range []struct{ name, expression, code, message string }{
		{"function", "pl_missing_function()", "42883", "function pl_missing_function() does not exist"},
		{"relation", "(SELECT x FROM pl_missing_relation)", "42P01", `relation "pl_missing_relation" does not exist`},
		{"division", "1/0", "22012", "division by zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION pl_unreached() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN RETURN 7; x := "+tc.expression+"; END$$",
					"CREATE FUNCTION pl_reached() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN "+tc.expression+"; END$$")
				plpgsqlValue(t, p, b, c, "SELECT pl_unreached() AS v", "INT4", int64(7))
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pl_reached()", "RETURN")
				switch tc.name {
				case "function":
					want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					want.InternalPosition, want.InternalQuery = "1", tc.expression
				case "relation":
					want.InternalPosition, want.InternalQuery = "16", tc.expression
				case "division":
					want.Context = "PL/pgSQL expression \"1/0\"\n" + want.Context
				}
				plpgsqlError(t, p, c, "SELECT pl_reached()", true, want)
			})
		})
	}
	for _, tc := range []struct{ body, message, position string }{
		{"DECLARE x integer; BEGIN RETURN 7; x := 1 + ; END", "syntax error at end of input", "114"},
		{"BEGIN RETURN 7; END; junk", `syntax error at or near "junk"`, "92"},
		{"BEGIN RETURN 7; END; BEGIN END", `syntax error at or near "BEGIN"`, "92"},
		{"<<a>> BEGIN RETURN 7; END b", `end label "b" differs from block's label "a"`, "97"},
		{"BEGIN BEGIN END RETURN 7; END", `end label "return" specified for unlabeled block`, "87"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
				want.Position = tc.position
				plpgsqlError(t, p, c, "CREATE FUNCTION pl_bad_syntax() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$", false, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='pl_bad_syntax'", "INT8", int64(0))
			})
		})
	}
	t.Run("entry_defaults", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE SEQUENCE pl_entry_seq",
				"CREATE FUNCTION pl_entry() RETURNS bigint LANGUAGE plpgsql AS $$DECLARE x bigint := nextval('pl_entry_seq'); BEGIN RETURN x; END$$")
			q := "SELECT pl_entry() AS v"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			plpgsqlValue(t, p, b, c, "SELECT is_called AS v FROM pl_entry_seq", "BOOL", false)
			for _, v := range []int64{1, 2} {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{v}}))
			}
		})
	})
}
