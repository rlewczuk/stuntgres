package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoutineProceduresDiagnostics(t *testing.T) {
	t.Run("declarations", func(t *testing.T) {
		for _, tc := range []struct{ name, args, attribute, code, message, at string }{
			{"variadic_out", "VARIADIC a integer[],OUT b integer", "", "42P13", "VARIADIC parameter must be the last parameter", "OUT b"},
			{"variadic_input", "VARIADIC a integer[],b integer", "", "42P13", "VARIADIC parameter must be the last input parameter", "b integer"},
			{"variadic_inout", "VARIADIC a integer[],INOUT b integer", "", "42P13", "VARIADIC parameter must be the last input parameter", "INOUT b"},
			{"default_out", "a integer DEFAULT 1,OUT b integer", "", "42P13", "procedure OUT parameters cannot appear after one with a default value", "OUT b"},
			{"out_default", "OUT a integer DEFAULT 1", "", "42P13", "only input parameters can have default values", "OUT a"},
			{"missing_default", "a integer DEFAULT 1,b integer", "", "42P13", "input parameters after one with a default value must also have defaults", "b integer"},
			{"nonarray", "VARIADIC a integer", "", "42P13", "VARIADIC parameter must be an array", "VARIADIC a"},
			{"set_argument", "a SETOF integer", "", "42P13", "procedures cannot accept set arguments", "a SETOF"},
			{"duplicate_input", "a integer,a integer", "", "42P13", `parameter name "a" used more than once`, "a integer)"},
			{"duplicate_output", "OUT a integer,OUT a integer", "", "42P13", `parameter name "a" used more than once`, "OUT a integer)"},
			{"overlap_inout", "IN a integer,INOUT a integer", "", "42P13", `parameter name "a" used more than once`, "INOUT a"},
			{"text_default", "a integer DEFAULT '1'::text", "", "42804", "argument of DEFAULT must be type integer, not type text", "'1'"},
			{"subquery_default", "a integer DEFAULT (SELECT 1)", "", "0A000", "cannot use subquery in DEFAULT expression", "(SELECT"},
			{"formal_default", "a integer,b integer DEFAULT a", "", "42703", `column "a" does not exist`, "a)"},
			{"strict", "", "STRICT", "42P13", "invalid attribute in procedure definition", "STRICT"},
			{"called_null", "", "CALLED ON NULL INPUT", "42P13", "invalid attribute in procedure definition", "CALLED"},
			{"returns_null", "", "RETURNS NULL ON NULL INPUT", "42P13", "invalid attribute in procedure definition", "RETURNS"},
			{"immutable", "", "IMMUTABLE", "42P13", "invalid attribute in procedure definition", "IMMUTABLE"},
			{"stable", "", "STABLE", "42P13", "invalid attribute in procedure definition", "STABLE"},
			{"volatile", "", "VOLATILE", "42P13", "invalid attribute in procedure definition", "VOLATILE"},
			{"leakproof", "", "LEAKPROOF", "42P13", "invalid attribute in procedure definition", "LEAKPROOF"},
			{"cost", "", "COST 1", "42P13", "invalid attribute in procedure definition", "COST"},
			{"rows", "", "ROWS 1", "42P13", "invalid attribute in procedure definition", "ROWS"},
			{"support", "", "SUPPORT pc_support", "42P13", "invalid attribute in procedure definition", "SUPPORT"},
			{"parallel", "", "PARALLEL SAFE", "42P13", "invalid attribute in procedure definition", "PARALLEL"},
			{"window", "", "WINDOW", "42P13", "invalid attribute in procedure definition", "WINDOW"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					resultsExec(t, p, b, c, "CREATE TABLE pc_ddl_effect(v integer)")
					q := "CREATE PROCEDURE pc_bad(" + tc.args + ") LANGUAGE SQL " + tc.attribute + " AS 'INSERT INTO pc_ddl_effect VALUES(99); SELECT 1'"
					at := strings.Index(q, tc.at) + 1
					want := proceduresDiagnostic(tc.code, tc.message, at)
					resultsError(t, p, c, q, false, "execute", want)
					proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_bad'", 0)
					proceduresCount(t, p, b, c, "pc_ddl_effect", 0)
					resultsExec(t, p, b, c, "CREATE PROCEDURE pc_bad(OUT answer integer) LANGUAGE SQL AS 'SELECT 7'")
					proceduresBoth(t, p, b, c, "CALL pc_bad(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
				})
			})
		}
	})
	t.Run("calls", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE TABLE pc_diag_effect(v integer)",
				"CREATE PROCEDURE pc_req(a integer,b integer DEFAULT 2) LANGUAGE SQL AS 'INSERT INTO pc_diag_effect VALUES(a)'",
				"CREATE PROCEDURE pc_amb(a integer,b integer DEFAULT 1) LANGUAGE SQL AS 'SELECT 1'",
				"CREATE PROCEDURE pc_amb(a integer,c text DEFAULT 'x') LANGUAGE SQL AS 'SELECT 2'",
				"CREATE PROCEDURE pc_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 7'",
				"CREATE FUNCTION pc_scalar(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT x+1'",
				"CREATE PROCEDURE pc_noout(x integer) LANGUAGE SQL AS 'SELECT x'",
				"CREATE FUNCTION pc_kind(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT x'",
				"CREATE PROCEDURE pc_kind(x bigint) LANGUAGE SQL AS 'SELECT x'")
			ambiguous := proceduresDiagnostic("42725", "procedure pc_amb(integer) is not unique", 6)
			ambiguous.Hint = "Could not choose a best candidate procedure. You might need to add explicit type casts."
			function := proceduresDiagnostic("42809", "pc_scalar(integer) is not a procedure", 6)
			function.Hint = "To call a function, use SELECT."
			procedure := proceduresDiagnostic("42809", "pc_noout(integer) is a procedure", 8)
			procedure.Hint = "To call a procedure, use CALL."
			exactFunction := proceduresDiagnostic("42809", "pc_kind(integer) is not a procedure", 6)
			exactFunction.Hint = function.Hint
			exactProcedure := proceduresDiagnostic("42809", "pc_kind(bigint) is a procedure", 8)
			exactProcedure.Hint = procedure.Hint
			for _, tc := range []struct {
				q string
				d fixture.Diagnostic
			}{
				{"CALL pc_req(a=>1,a=>2)", proceduresDiagnostic("42601", `argument name "a" used more than once`, 18)},
				{"CALL pc_req(a=>1,2)", proceduresDiagnostic("42601", "positional argument cannot follow named argument", 18)},
				{"CALL pc_req()", proceduresMissing("pc_req()")},
				{"CALL pc_req(z=>1)", proceduresMissing("pc_req(z => integer)")},
				{"CALL pc_req(1,a=>2)", proceduresMissing("pc_req(integer, a => integer)")},
				{"CALL pc_req('1'::text)", proceduresMissing("pc_req(text)")},
				{"CALL pc_amb(1)", ambiguous},
				{"CALL pc_scalar(1)", function},
				{"SELECT pc_noout(1)", procedure},
				{"CALL pc_kind(1)", exactFunction},
				{"SELECT pc_kind(1::bigint)", exactProcedure},
				{"CALL pc_out((SELECT 1))", proceduresDiagnostic("0A000", "cannot use subquery in CALL argument", 13)},
				{"CALL pc_out(generate_series(1,2))", proceduresDiagnostic("0A000", "set-returning functions are not allowed in CALL arguments", 13)},
				{"CALL pc_out(sum(1))", proceduresDiagnostic("42803", "aggregate functions are not allowed in CALL arguments", 13)},
				{"CALL pc_out(row_number() OVER ())", proceduresDiagnostic("42P20", "window functions are not allowed in CALL arguments", 13)},
				{"CALL pc_out(pc_missing())", func() fixture.Diagnostic {
					d := proceduresDiagnostic("42883", "function pc_missing() does not exist", 13)
					d.Hint = resultsNoMatchHint
					return d
				}()},
				{"CALL pc_out(NULL::pc_missing_type)", proceduresDiagnostic("42704", `type "pc_missing_type" does not exist`, 19)},
			} {
				resultsError(t, p, c, tc.q, true, "prepare", tc.d)
				proceduresCount(t, p, b, c, "pc_diag_effect", 0)
			}
			// Named labels disambiguate the common default-expansion ambiguity.
			for _, q := range []string{"CALL pc_amb(a=>1,b=>2)", "CALL pc_amb(a=>1,c=>'x')"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
			}
			proceduresBoth(t, p, b, c, "CALL pc_out(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
		})
	})
	t.Run("identity_and_legal_function_contrasts", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE FUNCTION pc_collision(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT x+1'",
				"CREATE PROCEDURE pc_existing(x integer) LANGUAGE SQL AS 'SELECT x'",
				"CREATE FUNCTION pc_function_default(a integer DEFAULT 1,OUT b integer) LANGUAGE SQL AS 'SELECT a'",
				"CREATE FUNCTION pc_function_variadic(VARIADIC a integer[],OUT b integer) LANGUAGE SQL AS 'SELECT 1'",
				"CREATE PROCEDURE pc_duplicate_sql(IN a integer,OUT a integer) LANGUAGE SQL AS 'SELECT $1+1'")
			d := proceduresDiagnostic("42723", `function "pc_collision" already exists with same argument types`, 0)
			resultsError(t, p, c, "CREATE PROCEDURE pc_collision(IN renamed integer,OUT answer text) LANGUAGE SQL AS $$SELECT 'x'::text$$", false, "execute", d)
			d = proceduresDiagnostic("42809", "cannot change routine kind", 0)
			d.Detail = `"pc_collision" is a function.`
			resultsError(t, p, c, "CREATE OR REPLACE PROCEDURE pc_collision(x integer) LANGUAGE SQL AS 'SELECT x'", false, "execute", d)
			d.Detail = `"pc_existing" is a procedure.`
			resultsError(t, p, c, "CREATE OR REPLACE FUNCTION pc_existing(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT x'", false, "execute", d)
			resultsBoth(t, p, b, c, "SELECT pc_collision(4) AS v,pc_function_default() AS d,pc_function_variadic(1,2) AS a", []string{"v", "d", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(5), int64(1), int64(1)}})
			proceduresBoth(t, p, b, c, "CALL pc_duplicate_sql(4,NULL)", []string{"a"}, []string{"INT4"}, [][]any{{int64(5)}})
			q := "CREATE PROCEDURE pc_bad_return() RETURNS integer LANGUAGE SQL AS 'SELECT 1'"
			resultsError(t, p, c, q, false, "prepare", proceduresDiagnostic("42601", `syntax error at or near "integer"`, strings.Index(q, "integer")+1))
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_bad_return'", 0)
			resultsExec(t, p, b, c, "CREATE PROCEDURE pc_bad_return() LANGUAGE SQL AS 'SELECT 1'")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CALL pc_bad_return()", 0))
		})
	})
	t.Run("all_kind_projection", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE FUNCTION pc_function_full(IN x integer,OUT answer integer) LANGUAGE SQL AS 'SELECT x+1'",
				"CREATE FUNCTION pc_function_variadic_full(VARIADIC xs integer[],OUT answer integer) LANGUAGE SQL AS 'SELECT 7'")
			for _, tc := range []struct {
				q, signature string
				missing      bool
			}{
				{"CALL pc_function_full(1)", "pc_function_full(integer)", true},
				{"CALL pc_function_full(1,NULL)", "pc_function_full(integer, unknown)", false},
				{"CALL pc_function_full(answer=>NULL,x=>1)", "pc_function_full(answer => unknown, x => integer)", false},
				{"CALL pc_function_variadic_full(ARRAY[1],2,3)", "pc_function_variadic_full(integer[], integer, integer)", false},
				{"CALL pc_function_variadic_full(ARRAY[1],VARIADIC 2)", "pc_function_variadic_full(integer[], integer)", false},
				{"CALL pc_function_variadic_full(xs=>ARRAY[1],VARIADIC answer=>2)", "pc_function_variadic_full(xs => integer[], answer => integer)", false},
				{"CALL pc_function_variadic_full(1,2)", "pc_function_variadic_full(integer, integer)", true},
				{"CALL pc_function_variadic_full(xs=>ARRAY[1],answer=>2)", "pc_function_variadic_full(xs => integer[], answer => integer)", true},
				{"CALL json_array_elements('[]'::json)", "json_array_elements(json)", true},
				{"CALL json_array_elements('[]'::json,NULL)", "json_array_elements(json, unknown)", false},
			} {
				want := proceduresMissing(tc.signature)
				if !tc.missing {
					want = proceduresDiagnostic("42809", tc.signature+" is not a procedure", 6)
					want.Hint = "To call a function, use SELECT."
				}
				resultsError(t, p, c, tc.q, true, "prepare", want)
			}
		})
	})
}
