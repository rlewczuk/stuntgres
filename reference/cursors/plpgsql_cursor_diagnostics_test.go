package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorDiagnostics(t *testing.T) {
	// Positions are outer-query character positions observed on pinned 18.6;
	// the complete absence of other optional compile fields is also asserted.
	compile := []struct{ name, decl, body, code, message, position string }{
		{"wrong_type", "x text;", "OPEN x FOR SELECT 7;", "42804", `variable "x" must be of type cursor or refcursor`, "88"},
		{"unknown_variable", "", "CLOSE x;", "42601", `"x" is not a known variable`, "82"},
		{"non_simple", "r pc_rows%ROWTYPE;", "CLOSE r.i;", "42804", "cursor variable must be a simple variable", "100"},
		{"unbound_for", "c refcursor;", "FOR r IN c LOOP END LOOP;", "42601", "cursor FOR loop must use a bound cursor variable", "97"},
		{"missing_args", "c CURSOR(a integer) FOR SELECT a;", "OPEN c;", "42601", `cursor "c" has arguments`, "115"},
		{"unexpected_args", "c CURSOR FOR SELECT 7;", "OPEN c(7);", "42601", `cursor "c" has no arguments`, "104"},
		{"unknown_arg", "c CURSOR(a integer) FOR SELECT a;", "OPEN c(x=>7);", "42601", `cursor "c" has no argument named "x"`, "116"},
		{"too_few", "c CURSOR(a integer,b integer) FOR SELECT a+b;", "OPEN c(7);", "42601", `not enough arguments for cursor "c"`, "129"},
		{"too_many", "c CURSOR(a integer) FOR SELECT a;", "OPEN c(7,8);", "42601", `too many arguments for cursor "c"`, "117"},
		{"duplicate_arg", "c CURSOR(a integer,b integer) FOR SELECT a+b;", "OPEN c(a=>7,a=>8);", "42601", `value for parameter "a" of cursor "c" specified more than once`, "133"},
		{"fetch_count", "c refcursor; x integer;", "FETCH FORWARD 1 FROM c INTO x;", "0A000", "FETCH statement cannot return multiple rows", "99"},
		{"fetch_all", "c refcursor; x integer;", "FETCH ALL FROM c INTO x;", "0A000", "FETCH statement cannot return multiple rows", "99"},
		{"fetch_backward_count", "c refcursor; x integer;", "FETCH BACKWARD 1 FROM c INTO x;", "0A000", "FETCH statement cannot return multiple rows", "99"},
		{"constant_target", "c refcursor; x CONSTANT integer:=0;", "FETCH c INTO x;", "22005", `variable "x" is declared CONSTANT`, "124"},
		{"loop_record_out_of_scope", "c CURSOR FOR SELECT 7 AS i;", "FOR r IN c LOOP END LOOP; r.i:=7;", "42601", `"r.i" is not a known variable`, "129"},
		{"suffix", "c refcursor;", "OPEN c FOR SELECT 7; END; trailing", "42601", `syntax error at or near "trailing"`, "114"},
	}
	for _, tc := range compile {
		t.Run("compile_"+tc.name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				q := "CREATE FUNCTION pc_bad() RETURNS void LANGUAGE plpgsql AS $$DECLARE " + tc.decl + " BEGIN " + tc.body + " END$$"
				want := sqlCursorDiagnostic(tc.code, tc.message)
				want.Position = tc.position
				plCursorError(t, p, b, c, q, want, false, false)
			})
		})
	}
	runtime := []struct{ name, decl, body, code, message, action, hint, detail string }{
		{"null_fetch", "c refcursor; x integer;", "FETCH c INTO x;", "22004", `cursor variable "c" is null`, "FETCH", "", ""},
		{"null_move", "c refcursor;", "MOVE c;", "22004", `cursor variable "c" is null`, "MOVE", "", ""},
		{"null_close", "c refcursor;", "CLOSE c;", "22004", `cursor variable "c" is null`, "CLOSE", "", ""},
		{"closed_fetch", "c refcursor:='pc_missing'; x integer;", "FETCH c INTO x;", "34000", `cursor "pc_missing" does not exist`, "FETCH", "", ""},
		{"closed_move", "c refcursor:='pc_missing';", "MOVE c;", "34000", `cursor "pc_missing" does not exist`, "MOVE", "", ""},
		{"closed_close", "c refcursor:='pc_missing';", "CLOSE c;", "34000", `cursor "pc_missing" does not exist`, "CLOSE", "", ""},
		{"no_scroll", "c refcursor; x integer;", "OPEN c NO SCROLL FOR SELECT i FROM pc_rows ORDER BY i; FETCH c INTO x; FETCH PRIOR FROM c INTO x;", "55000", "cursor can only scan forward", "FETCH", "Declare it with SCROLL option to enable backward scan.", ""},
		{"no_scroll_absolute", "c refcursor; x integer;", "OPEN c NO SCROLL FOR SELECT i FROM pc_rows ORDER BY i; FETCH c INTO x; FETCH ABSOLUTE 1 FROM c INTO x;", "55000", "cursor can only scan forward", "FETCH", "Declare it with SCROLL option to enable backward scan.", ""},
		{"scroll_row_lock", "c refcursor;", "OPEN c SCROLL FOR SELECT i FROM pc_rows FOR UPDATE;", "0A000", "DECLARE SCROLL CURSOR ... FOR UPDATE/SHARE is not supported", "OPEN", "", "Scrollable cursors must be READ ONLY."},
		{"default_row_lock", "c refcursor; x integer;", "OPEN c FOR SELECT i FROM pc_rows ORDER BY i FOR UPDATE; FETCH c INTO x; FETCH PRIOR FROM c INTO x;", "55000", "cursor can only scan forward", "FETCH", "Declare it with SCROLL option to enable backward scan.", ""},
		{"null_command", "c refcursor;", "OPEN c FOR EXECUTE NULL::text;", "22004", "query string argument of EXECUTE is null", "OPEN", "", ""},
		{"null_absolute", "c refcursor; x integer; n integer;", "OPEN c SCROLL FOR SELECT 7; FETCH ABSOLUTE n FROM c INTO x;", "22004", "relative or absolute cursor position is null", "FETCH", "", ""},
		{"null_relative", "c refcursor; x integer; n integer;", "OPEN c SCROLL FOR SELECT 7; FETCH RELATIVE n FROM c INTO x;", "22004", "relative or absolute cursor position is null", "FETCH", "", ""},
		{"null_move_count", "c refcursor; n integer;", "OPEN c FOR SELECT 7; MOVE FORWARD n FROM c;", "22004", "relative or absolute cursor position is null", "MOVE", "", ""},
		{"dynamic_multiquery", "c refcursor:='pc_failed';", "OPEN c FOR EXECUTE 'SELECT 7; SELECT 8';", "42P11", "cannot open multi-query plan as cursor", "OPEN", "", ""},
		{"dynamic_nonrow", "c refcursor:='pc_failed';", "OPEN c FOR EXECUTE 'INSERT INTO pc_rows VALUES(40,NULL)';", "42P11", "cannot open INSERT query as cursor", "OPEN", "", ""},
		{"dynamic_no_substitution", "c refcursor:='pc_failed'; local_only integer:=7;", "OPEN c FOR EXECUTE 'SELECT local_only';", "42703", `column "local_only" does not exist`, "OPEN", "", ""},
		{"dynamic_parse", "c refcursor:='pc_failed';", "OPEN c FOR EXECUTE 'SELECT FROM';", "42601", "syntax error at end of input", "OPEN", "", ""},
		{"dynamic_bind", "c refcursor:='pc_failed';", "OPEN c FOR EXECUTE 'SELECT $2::integer' USING 7;", "42P02", "there is no parameter $2", "OPEN", "", ""},
		{"target_conversion", "c refcursor; x integer;", "OPEN c FOR SELECT 'not_int'::text; FETCH c INTO x;", "22P02", `invalid input syntax for type integer: "not_int"`, "FETCH", "", ""},
		{"target_notnull", "c refcursor; x integer NOT NULL:=7;", "OPEN c FOR SELECT NULL::integer; FETCH c INTO x;", "22004", `null value cannot be assigned to variable "x" declared NOT NULL`, "FETCH", "", ""},
		{"target_typmod", "c refcursor; x varchar(3);", "OPEN c FOR SELECT 'long'::text; FETCH c INTO x;", "22001", "value too long for type character varying(3)", "FETCH", "", ""},
		{"count_overflow", "c refcursor; x integer;", "OPEN c SCROLL FOR SELECT 7; FETCH ABSOLUTE 9223372036854775808::numeric FROM c INTO x;", "22003", "integer out of range", "FETCH", "", ""},
		{"minimum_absolute", "c refcursor; x integer;", "OPEN c SCROLL FOR SELECT 7; FETCH ABSOLUTE (-9223372036854775807::bigint-1) FROM c INTO x;", "22003", "integer out of range", "FETCH", "", ""},
		{"minimum_move", "c refcursor;", "OPEN c SCROLL FOR SELECT 7; MOVE FORWARD (-9223372036854775807::bigint-1) FROM c;", "22003", "integer out of range", "MOVE", "", ""},
		{"empty_name", "c refcursor:='';", "OPEN c FOR SELECT 7;", "42P03", `cursor "" already in use`, "OPEN", "", ""},
		{"duplicate_loop", "c CURSOR FOR SELECT i FROM pc_rows ORDER BY i;", "c:='pc_duplicate'; OPEN c; FOR r IN c LOOP END LOOP;", "42P03", `cursor "pc_duplicate" already in use`, "FOR over cursor", "", ""},
	}
	for _, tc := range runtime {
		t.Run("runtime_"+tc.name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				plCursorFunction(t, p, b, c, "pc_bad", "", "void", "DECLARE "+tc.decl+" BEGIN "+tc.body+" END")
				want := plCursorRuntime(tc.code, tc.message, "pc_bad()", tc.action)
				want.Hint = tc.hint
				want.Detail = tc.detail
				switch tc.name {
				case "scroll_row_lock":
					want.Context = "SQL statement \"SELECT i FROM pc_rows FOR UPDATE\"\n" + want.Context
				case "dynamic_no_substitution":
					want.InternalQuery = "SELECT local_only"
					want.InternalPosition = "8"
				case "dynamic_parse":
					want.InternalQuery = "SELECT FROM"
					want.InternalPosition = "12"
				case "dynamic_bind":
					want.InternalQuery = "SELECT $2::integer"
					want.InternalPosition = "8"
				}
				plCursorError(t, p, b, c, "SELECT pc_bad() AS v", want, true, false)
				plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='pc_failed'", "INT8", int64(0))
				plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pc_rows", "INT8", int64(3))
			})
		})
	}
	t.Run("domain_assignment", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorStep(t, p, b, c, fixture.Exec("CREATE DOMAIN pc_positive AS integer CHECK(VALUE>0)", 0))
			plCursorFunction(t, p, b, c, "pc_bad", "", "void", "DECLARE c refcursor; x pc_positive; BEGIN OPEN c FOR SELECT -1; FETCH c INTO x; END")
			want := plCursorRuntime("23514", "value for domain pc_positive violates check constraint \"pc_positive_check\"", "pc_bad()", "FETCH")
			want.DataType = "pc_positive"
			want.Schema = "public"
			want.Constraint = "pc_positive_check"
			plCursorError(t, p, b, c, "SELECT pc_bad() AS v", want, true, false)
		})
	})
	t.Run("duplicate_preserves_original_and_abort", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plCursorRows(t, p, b, c)
			plCursorHelpers(t, p, b, c)
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_open('pc_duplicate'::refcursor) AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"pc_duplicate"}}))
			plCursorError(t, p, b, c, "SELECT pc_open('pc_duplicate'::refcursor) AS v", plCursorRuntime("42P03", `cursor "pc_duplicate" already in use`, "pc_open(refcursor)", "OPEN"), true, true)
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT pc_take('pc_duplicate'::refcursor) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			want := plCursorMissing("pc_absent")
			s := fixture.ErrorDiagnostic("SELECT pc_take('pc_absent'::refcursor) AS v", want)
			s.SelectRows = true
			sqlCursorStep(t, p, b, c, s)
			sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("SELECT 1", sqlCursorDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			plCursorValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
		})
	})
}
