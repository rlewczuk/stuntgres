package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSQLExecutionCallDestinations(t *testing.T) {
	t.Run("frames_and_scalar_null", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE sx_do_result(i integer)",
				"CREATE PROCEDURE sx_sql_bump(INOUT x integer) LANGUAGE SQL AS 'SELECT x+1'",
				"CREATE PROCEDURE sx_pl_bump(INOUT x integer) LANGUAGE plpgsql AS $$BEGIN x:=x+1; END$$",
				"CREATE FUNCTION sx_bump(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN CALL sx_sql_bump(x); RETURN x; END$$",
				"CREATE FUNCTION sx_bump_pl(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN CALL sx_pl_bump(x); RETURN x; END$$",
				"CREATE PROCEDURE sx_outer(INOUT x integer) LANGUAGE plpgsql AS $$BEGIN CALL sx_sql_bump(x); END$$")
			for _, name := range []string{"sx_bump", "sx_bump_pl"} {
				for _, tc := range []struct{ in, out any }{{4, int64(5)}, {8, int64(9)}, {nil, nil}} {
					sqlExecutionValue(t, p, b, c, "SELECT "+name+"($1::integer) AS v", "INT4", tc.out, tc.in)
				}
			}
			sqlExecutionBoth(t, p, b, c, "CALL sx_outer($1::integer)", []string{"x"}, []string{"INT4"}, [][]any{{int64(5)}}, 4)
			sqlExecutionBoth(t, p, b, c, "CALL sx_outer($1::integer)", []string{"x"}, []string{"INT4"}, [][]any{{nil}}, nil)
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("DO $$DECLARE x integer:=4; BEGIN CALL sx_pl_bump(x); INSERT INTO sx_do_result VALUES(x); END$$", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM sx_do_result", "INT4", int64(5))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sx_do_result", 1))
			}
		})
	})
	t.Run("fields_aliases_and_overlap", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE sx_pair AS (n integer,t text)", "CREATE TYPE sx_int_pair AS (a integer,b integer)",
				"CREATE PROCEDURE sx_sql_bump(INOUT x integer) LANGUAGE SQL AS 'SELECT x+1'",
				"CREATE PROCEDURE sx_seven(OUT x integer) LANGUAGE SQL AS 'SELECT 7'",
				"CREATE PROCEDURE sx_two(OUT a integer,OUT b integer) LANGUAGE SQL AS 'SELECT 7,8'",
				"CREATE FUNCTION sx_field() RETURNS text LANGUAGE plpgsql AS $$DECLARE r sx_pair:=ROW(4,'keep'); BEGIN CALL sx_sql_bump(r.n); RETURN r.n||':'||r.t; END$$",
				"CREATE FUNCTION sx_qualified_field() RETURNS text LANGUAGE plpgsql AS $$<<blk>> DECLARE r sx_pair:=ROW(4,'keep'); BEGIN CALL sx_sql_bump(blk.r.n); RETURN r.n||':'||r.t; END$$",
				"CREATE FUNCTION sx_alias(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE a ALIAS FOR $1; BEGIN CALL sx_sql_bump(a); CALL sx_sql_bump($1); RETURN x; END$$",
				"CREATE FUNCTION sx_record() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; BEGIN SELECT 4 AS n,'keep'::text AS t INTO r; CALL sx_sql_bump(r.n); RETURN r.n||':'||r.t; END$$",
				"CREATE FUNCTION sx_null_field() RETURNS text LANGUAGE plpgsql AS $$DECLARE r sx_pair; BEGIN CALL sx_seven(r.n); RETURN r.n||':'||coalesce(r.t,'NULL'); END$$",
				"CREATE FUNCTION sx_siblings() RETURNS text LANGUAGE plpgsql AS $$DECLARE r sx_int_pair:=ROW(10,20); BEGIN CALL sx_two(r.a,r.b); RETURN r.a||':'||r.b; END$$",
				"CREATE FUNCTION sx_repeat() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=10; BEGIN CALL sx_two(x,x); RETURN x; END$$")
			for _, name := range []string{"sx_field", "sx_qualified_field", "sx_record"} {
				sqlExecutionValue(t, p, b, c, "SELECT "+name+"() AS v", "TEXT", "5:keep")
			}
			sqlExecutionValue(t, p, b, c, "SELECT sx_alias(4) AS v", "INT4", int64(6))
			sqlExecutionValue(t, p, b, c, "SELECT sx_null_field() AS v", "TEXT", "7:NULL")
			sqlExecutionValue(t, p, b, c, "SELECT sx_siblings() AS v", "TEXT", "7:8")
			sqlExecutionValue(t, p, b, c, "SELECT sx_repeat() AS v", "INT4", int64(8))
			sqlExecutionBoth(t, p, b, c, "CALL sx_seven(NULL)", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}})
		})
	})
	for _, tc := range []struct{ name, declarations, target, code, message string }{
		{"literal", "", "4", "42601", `procedure parameter "answer" is an output parameter but corresponding argument is not writable`},
		{"null", "", "NULL", "42601", `procedure parameter "answer" is an output parameter but corresponding argument is not writable`},
		{"arithmetic", "x integer:=4;", "x+1", "42601", `procedure parameter "answer" is an output parameter but corresponding argument is not writable`},
		{"element", "a integer[]:=ARRAY[4];", "a[1]", "42601", `procedure parameter "answer" is an output parameter but corresponding argument is not writable`},
		{"slice", "a integer[]:=ARRAY[4];", "a[1:1]", "42601", `procedure parameter "answer" is an output parameter but corresponding argument is not writable`},
		{"constant", "x CONSTANT integer:=4;", "x", "22005", `variable "x" is declared CONSTANT`},
		{"coerced_variable", "x smallint:=4;", "x", "42601", `procedure parameter "answer" is an output parameter but corresponding argument is not writable`},
	} {
		t.Run("nonwritable_"+tc.name, func(t *testing.T) {
			sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				typ := "integer"
				value := "7"
				if tc.name == "slice" {
					typ, value = "integer[]", "ARRAY[7]"
				}
				userOperatorExec(t, p, b, c, "CREATE SEQUENCE sx_input_seq", "CREATE TABLE sx_call_marks(i integer)",
					"CREATE PROCEDURE sx_target(IN x integer,OUT answer "+typ+") LANGUAGE SQL AS 'INSERT INTO sx_call_marks VALUES(x); SELECT "+value+"'",
					"CREATE FUNCTION sx_invalid() RETURNS integer LANGUAGE plpgsql AS $$DECLARE "+tc.declarations+" BEGIN CALL sx_target(nextval('sx_input_seq')::integer,"+tc.target+"); RETURN 1; END$$")
				sqlExecutionCallError(t, p, c, "SELECT sx_invalid() AS v", "sx_invalid()", tc.code, tc.message)
				sqlExecutionCount(t, p, b, c, "sx_call_marks", 0)
				sqlExecutionValue(t, p, b, c, "SELECT is_called IS TRUE AS v FROM sx_input_seq", "BOOL", false)
				userOperatorExec(t, p, b, c,
					"CREATE PROCEDURE sx_target_default(OUT answer "+typ+",IN x integer DEFAULT nextval('sx_input_seq')::integer) LANGUAGE SQL AS 'INSERT INTO sx_call_marks VALUES(x); SELECT "+value+"'",
					"CREATE OR REPLACE FUNCTION sx_invalid() RETURNS integer LANGUAGE plpgsql AS $$DECLARE "+tc.declarations+" BEGIN CALL sx_target_default("+tc.target+"); RETURN 1; END$$")
				sqlExecutionCallError(t, p, c, "SELECT sx_invalid() AS v", "sx_invalid()", tc.code, tc.message)
				sqlExecutionCount(t, p, b, c, "sx_call_marks", 0)
				sqlExecutionValue(t, p, b, c, "SELECT is_called IS TRUE AS v FROM sx_input_seq", "BOOL", false)
			})
		})
	}
	t.Run("unnamed_and_unassigned_record", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE PROCEDURE sx_unnamed(IN integer,OUT integer) LANGUAGE SQL AS 'SELECT 7'",
				"CREATE PROCEDURE sx_seven(OUT x integer) LANGUAGE SQL AS 'SELECT 7'",
				"CREATE FUNCTION sx_ordinal() RETURNS integer LANGUAGE plpgsql AS $$BEGIN CALL sx_unnamed(1,NULL); RETURN 1; END$$",
				"CREATE FUNCTION sx_unassigned() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN CALL sx_seven(r.n); RETURN 1; END$$")
			sqlExecutionCallError(t, p, c, "SELECT sx_ordinal() AS v", "sx_ordinal()", "42601", "procedure parameter 2 is an output parameter but corresponding argument is not writable")
			d := plpgsqlRuntimeDiagnostic("55000", `record "r" is not assigned yet`, "sx_unassigned()", "CALL")
			d.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
			d.Context = "SQL statement \"CALL sx_seven(r.n)\"\n" + d.Context
			plpgsqlError(t, p, c, "SELECT sx_unassigned() AS v", true, d)
		})
	})
	t.Run("common_resolution_errors", func(t *testing.T) {
		for _, tc := range []struct{ name, setup, call, code, message, hint string }{
			{"missing", "", "CALL sx_absent(x)", "42883", "procedure sx_absent(integer) does not exist", "No procedure matches the given name and argument types. You might need to add explicit type casts."},
			{"wrong_kind", "CREATE FUNCTION sx_callable(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT x'", "CALL sx_callable(x)", "42809", "sx_callable(integer) is not a procedure", "To call a function, use SELECT."},
			{"wrong_type", "CREATE PROCEDURE sx_callable(INOUT x integer) LANGUAGE SQL AS 'SELECT x'", "CALL sx_callable('bad'::text)", "42883", "procedure sx_callable(text) does not exist", "No procedure matches the given name and argument types. You might need to add explicit type casts."},
		} {
			t.Run(tc.name, func(t *testing.T) {
				sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					if tc.setup != "" {
						userOperatorExec(t, p, b, c, tc.setup)
					}
					userOperatorExec(t, p, b, c, "CREATE FUNCTION sx_resolution() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=4; BEGIN "+tc.call+"; RETURN x; END$$")
					d := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "sx_resolution()", "CALL")
					d.Hint, d.InternalQuery, d.InternalPosition = tc.hint, tc.call, "6"
					plpgsqlError(t, p, c, "SELECT sx_resolution() AS v", true, d)
				})
			})
		}
	})
}
