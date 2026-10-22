package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineParametersDiagnostics(t *testing.T) {
	t.Run("declarations", func(t *testing.T) {
		parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// Complete diagnostic fields and stages observed on pinned18.6 in tests-3.md.
			for _, tc := range []struct{ name, declaration, code, message, corrected, call, position string }{
				{"nonarray", "VARIADIC a int) RETURNS int", "42P13", "VARIADIC parameter must be an array", "a int) RETURNS int", "1", "24"},
				{"after variadic", "VARIADIC a int[],b int) RETURNS int", "42P13", "VARIADIC parameter must be the last input parameter", "a int[],b int) RETURNS int", "'{}'::int[],1", "41"},
				{"two variadics", "VARIADIC a int[],VARIADIC b int[]) RETURNS int", "42P13", "VARIADIC parameter must be the last input parameter", "a int[],b int[]) RETURNS int", "'{}'::int[],'{}'::int[]", "41"},
				{"out default", "OUT a int DEFAULT 1)", "42P13", "only input parameters can have default values", "OUT a int)", "", "24"},
				{"missing trailing default", "a int DEFAULT 1,b int) RETURNS int", "42P13", "input parameters after one with a default value must also have defaults", "a int DEFAULT 1,b int DEFAULT 2) RETURNS int", "", "40"},
				{"out interposed", "a int DEFAULT 1,OUT y int,b int)", "42P13", "input parameters after one with a default value must also have defaults", "a int DEFAULT 1,OUT y int,b int DEFAULT 2)", "", "50"},
				{"duplicate input", "a int,a int) RETURNS int", "42P13", `parameter name "a" used more than once`, "a int,b int) RETURNS int", "1,2", "30"},
				{"inout overlaps input", "IN a int,INOUT a int)", "42P13", `parameter name "a" used more than once`, "IN x int,INOUT a int)", "1,2", "33"},
				{"inout overlaps output", "INOUT a int,OUT a int)", "42P13", `parameter name "a" used more than once`, "INOUT a int)", "1", "36"},
				{"mismatched result", "OUT a int) RETURNS bigint", "42P13", "function result type must be integer because of OUT parameters", "OUT a int) RETURNS int", "", ""},
				{"missing result", "a int)", "42P13", "function result type must be specified", "a int) RETURNS int", "1", ""},
				{"text default", "a int DEFAULT '1'::text) RETURNS int", "42804", "argument of DEFAULT must be type integer, not type text", "a int DEFAULT 1) RETURNS int", "", "38"},
				{"subquery default", "a int DEFAULT (SELECT 1)) RETURNS int", "0A000", "cannot use subquery in DEFAULT expression", "a int DEFAULT 1) RETURNS int", "", "38"},
				{"aggregate default", "a int DEFAULT sum(1)) RETURNS int", "42803", "aggregate functions are not allowed in DEFAULT expressions", "a int DEFAULT 1) RETURNS int", "", "38"},
				{"window default", "a bigint DEFAULT row_number() OVER ()) RETURNS int", "42P20", "window functions are not allowed in DEFAULT expressions", "a bigint DEFAULT 1) RETURNS int", "", "41"},
				{"formal default", "a int,b int DEFAULT a) RETURNS int", "42703", `column "a" does not exist`, "a int,b int DEFAULT 1) RETURNS int", "1", "44"},
				{"out numbering", "OUT y int,IN x int) RETURNS int", "42P02", "there is no parameter $2", "OUT y int,IN x int) RETURNS int", "1", "80"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := "SELECT 1"
					if tc.name == "out numbering" {
						body = "SELECT $2"
					}
					q := "CREATE FUNCTION rp_bad(" + tc.declaration + " LANGUAGE SQL AS '" + body + "'"
					parametersError(t, p, c, q, false, tc.code, tc.message, "", tc.position)
					userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rp_bad'", "INT8", int64(0))
					userOperatorExec(t, p, b, c, "CREATE FUNCTION rp_bad("+tc.corrected+" LANGUAGE SQL AS 'SELECT 1'")
					parametersValue(t, p, b, c, "SELECT rp_bad("+tc.call+") AS v", "v", "INT4", int64(1))
					userOperatorExec(t, p, b, c, "DROP FUNCTION rp_bad")
				})
			}
		})
	})
	t.Run("calls", func(t *testing.T) {
		parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION rp_required(a int,b int DEFAULT 2) RETURNS int LANGUAGE SQL AS 'SELECT $1+$2'",
				"CREATE FUNCTION rp_narrow(a int DEFAULT 1::bigint) RETURNS int LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION rp_no_tail(VARIADIC xs int[]) RETURNS int[] LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION rp_one_out(IN x int,OUT y int) LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION rp_amb(a int,b int DEFAULT 1) RETURNS int LANGUAGE SQL AS 'SELECT 1'",
				"CREATE FUNCTION rp_amb(a int,c text DEFAULT 'd') RETURNS int LANGUAGE SQL AS 'SELECT 2'")
			for _, tc := range []struct{ q, code, message, hint, position string }{
				{"SELECT rp_required(a=>1,a=>2)", "42601", `argument name "a" used more than once`, "", "25"},
				{"SELECT rp_required(a=>1,2)", "42601", "positional argument cannot follow named argument", "", "25"},
				{"SELECT rp_required()", "42883", "function rp_required() does not exist", parametersNoMatchHint, "8"},
				{"SELECT rp_required(z=>1)", "42883", "function rp_required(z => integer) does not exist", parametersNoMatchHint, "8"},
				{"SELECT rp_required(1,a=>2)", "42883", "function rp_required(integer, a => integer) does not exist", parametersNoMatchHint, "8"},
				{"SELECT rp_one_out(1,2)", "42883", "function rp_one_out(integer, integer) does not exist", parametersNoMatchHint, "8"},
				{"SELECT rp_no_tail()", "42883", "function rp_no_tail() does not exist", parametersNoMatchHint, "8"},
				{"SELECT rp_no_tail(xs=>1)", "42883", "function rp_no_tail(xs => integer) does not exist", parametersNoMatchHint, "8"},
				{"SELECT rp_amb(1)", "42725", "function rp_amb(integer) is not unique", parametersAmbiguousHint, "8"},
				{"SELECT rp_narrow('bad')", "22P02", `invalid input syntax for type integer: "bad"`, "", "18"},
				{"SELECT rp_narrow('1'::text)", "42883", "function rp_narrow(text) does not exist", parametersNoMatchHint, "8"},
				{"SELECT rp_narrow(1::bigint)", "42883", "function rp_narrow(bigint) does not exist", parametersNoMatchHint, "8"},
				{"SELECT 'é' AS label,rp_required()", "42883", "function rp_required() does not exist", parametersNoMatchHint, "21"},
			} {
				t.Run(tc.q, func(t *testing.T) { parametersError(t, p, c, tc.q, true, tc.code, tc.message, tc.hint, tc.position) })
			}
			// Bind conversion must not borrow the function-token Position from
			// parse analysis. Portal Context was observed on pinned18.6 in tests-3.md.
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			want.Context = "unnamed portal parameter $1 = '...'"
			fixture.EnumReferenceErrorChecked(t, p, c, "SELECT rp_narrow(a=>$1)", true, "execute", func(t *testing.T, got fixture.Diagnostic) {
				if got != want {
					t.Errorf("parameter bind diagnostic got %#v want %#v", got, want)
				}
			}, "bad")
			parametersValue(t, p, b, c, "SELECT rp_narrow() AS v", "v", "INT4", int64(1))
			parametersHeld(t, p, b, c, "SELECT rp_narrow(a=>$1::int) AS v", "INT4", [][]any{{7}, {8}}, []any{int64(7), int64(8)})
		})
	})
}
