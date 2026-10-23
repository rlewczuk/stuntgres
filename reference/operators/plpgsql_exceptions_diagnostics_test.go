package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLExceptionDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, setup, expression, code, message string }{
		{"division", "", "1/0", "22012", "division by zero"},
		{"cast", "", "'bad'::integer", "22P02", `invalid input syntax for type integer: "bad"`},
		{"unique", "CREATE FUNCTION ex_fail() RETURNS integer LANGUAGE SQL AS $$INSERT INTO ex_unique VALUES(1); SELECT 1$$", "ex_fail()", "23505", `duplicate key value violates unique constraint "ex_unique_pkey"`},
		{"not_null", "CREATE FUNCTION ex_fail() RETURNS integer LANGUAGE SQL AS $$INSERT INTO ex_not_null VALUES(NULL); SELECT 1$$", "ex_fail()", "23502", `null value in column "v" of relation "ex_not_null" violates not-null constraint`},
	} {
		for _, rethrow := range []bool{false, true} {
			name := tc.name + "_plain"
			if rethrow {
				name = tc.name + "_rethrow"
			}
			t.Run(name, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE TABLE ex_unique(v integer PRIMARY KEY)", "CREATE TABLE ex_not_null(v integer NOT NULL)")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_unique VALUES(1)", 1))
					if tc.setup != "" {
						userOperatorExec(t, p, b, c, tc.setup)
					}
					body := "DECLARE x integer; BEGIN x := " + tc.expression + "; RETURN x;"
					if rethrow {
						body += " EXCEPTION WHEN OTHERS THEN RAISE;"
					}
					plpgsqlExceptionDefine(t, p, b, c, "ex_diagnostic", "integer", body+" END")
					want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "ex_diagnostic()", "assignment")
					switch tc.name {
					case "division":
						want = plpgsqlExceptionDivision("ex_diagnostic()")
					case "cast":
						want.InternalPosition = "6"
						want.InternalQuery = "x := 'bad'::integer"
					case "unique":
						want.Context = "SQL function \"ex_fail\" statement 1\n" + want.Context
						want.Detail = "Key (v)=(1) already exists."
						want.Schema, want.Table, want.Constraint = "public", "ex_unique", "ex_unique_pkey"
					case "not_null":
						want.Context = "SQL function \"ex_fail\" statement 1\n" + want.Context
						want.Detail = "Failing row contains (null)."
						want.Schema, want.Table, want.Column = "public", "ex_not_null", "v"
					}
					plpgsqlError(t, p, c, "SELECT ex_diagnostic() AS v", true, want)
				})
			})
		}
	}
	t.Run("multiline_utf8", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plpgsqlExceptionDefine(t, p, b, c, "ex_utf8", "integer", `DECLARE
 s text := 'é中';
 x integer;
BEGIN
 x := length(s);
 x := 'bad'::integer;
 RETURN x;
EXCEPTION WHEN OTHERS THEN
 RAISE;
END`)
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			want.InternalPosition = "6"
			want.InternalQuery = "x := 'bad'::integer"
			want.Context = "PL/pgSQL function ex_utf8() line 6 at assignment"
			plpgsqlError(t, p, c, "SELECT ex_utf8() AS v", true, want)
		})
	})
}
