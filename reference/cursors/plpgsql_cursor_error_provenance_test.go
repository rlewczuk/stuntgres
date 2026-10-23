package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorErrorProvenance(t *testing.T) {
	run := func(t *testing.T, scenario func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			scenario(t, p, b, c)
		})
	}
	cases := []struct {
		name, declarations, statement, action, line, code, message, internal, prefix, position string
	}{
		{"static_missing", "c refcursor;", "OPEN c FOR SELECT missing_input;", "OPEN", "1", "42703", `column "missing_input" does not exist`, "SELECT missing_input", "", "8"},
		{"static_planning", "c refcursor;", "OPEN c FOR SELECT 1/0;", "OPEN", "1", "22012", "division by zero", "", `SQL statement "SELECT 1/0"`, ""},
		{"bound_query_missing", "c CURSOR FOR SELECT missing_input;", "OPEN c;", "OPEN", "1", "42703", `column "missing_input" does not exist`, "SELECT missing_input", "", "8"},
		{"bound_named_missing", "c CURSOR(a integer,b integer) FOR SELECT a+b;", "OPEN c(b=>missing_input,a=>7);", "OPEN", "1", "42703", `column "missing_input" does not exist`, "7 AS a, missing_input AS b", "", "9"},
		{"bound_quoted_named_missing", `c CURSOR(a integer,"Odd Name" integer) FOR SELECT a+"Odd Name";`, `OPEN c("Odd Name"=>missing_input,a=>7);`, "OPEN", "1", "42703", `column "missing_input" does not exist`, `7 AS a, missing_input AS "Odd Name"`, "", "9"},
		{"count_cast_after_expression", "c refcursor; x integer;", "OPEN c SCROLL FOR SELECT 7; FETCH ABSOLUTE 'bad'::text FROM c INTO x;", "FETCH", "1", "22P02", `invalid input syntax for type integer: "bad"`, "", "", ""},
		{"count_null_after_expression", "c refcursor; x integer; n integer;", "OPEN c SCROLL FOR SELECT 7; MOVE FORWARD n FROM c;", "MOVE", "1", "22004", "relative or absolute cursor position is null", "", "", ""},
	}
	for _, route := range []struct{ name, declarations, statement, action string }{
		{"bound_actual", "c CURSOR(a integer) FOR SELECT a; n integer:=0;", "OPEN c(%s);", "OPEN"},
		{"command", "c refcursor; n integer:=0;", "OPEN c FOR EXECUTE (%s)::text;", "OPEN"},
		{"using", "c refcursor; n integer:=0;", "OPEN c FOR EXECUTE 'SELECT $1::integer' USING %s;", "OPEN"},
		{"fetch_count", "c refcursor; x integer; n integer:=0;", "OPEN c SCROLL FOR SELECT 7; FETCH ABSOLUTE %s FROM c INTO x;", "FETCH"},
		{"move_count", "c refcursor; n integer:=0;", "OPEN c SCROLL FOR SELECT 7; MOVE FORWARD %s FROM c;", "MOVE"},
	} {
		for _, phase := range []struct {
			name, expression string
			trace            bool
		}{
			{"positioned", "missing_input", false},
			{"planning", "1/0", true},
			{"simple_runtime", "1/n", false},
			{"complex_runtime", "(SELECT 1/n)", true},
		} {
			expression := phase.expression
			if route.name == "command" {
				expression = "(" + expression + ")::text"
			}
			statement := fmt.Sprintf(route.statement, phase.expression)
			code, message, internal, prefix, position := "22012", "division by zero", "", "", ""
			if phase.name == "positioned" {
				code, message, internal = "42703", `column "missing_input" does not exist`, expression
				position = "1"
				if route.name == "command" {
					position = "2"
				}
			} else if phase.trace || route.name == "bound_actual" {
				prefix = "PL/pgSQL expression \"" + expression + "\""
			}
			cases = append(cases, struct{ name, declarations, statement, action, line, code, message, internal, prefix, position string }{route.name + "_" + phase.name, route.declarations, statement, route.action, "1", code, message, internal, prefix, position})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorFunction(t, p, b, c, "pc_origin", "", "void", "DECLARE "+tc.declarations+" BEGIN "+tc.statement+" END")
				want := sqlCursorDiagnostic(tc.code, tc.message)
				want.InternalQuery = tc.internal
				want.InternalPosition = tc.position
				want.Context = "PL/pgSQL function pc_origin() line " + tc.line + " at " + tc.action
				if tc.prefix != "" {
					want.Context = tc.prefix + "\n" + want.Context
				}
				plCursorError(t, p, b, c, "SELECT pc_origin() AS v", want, true, false)
				plCursorValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
			})
		})
	}
	for _, tc := range []struct{ name, body, internal, line, position string }{
		{"utf8_unbound_query", "DECLARE s text:='é中';\nc refcursor;\nBEGIN\nOPEN\n c FOR\n SELECT /*é中*/ missing_input;\nEND", "SELECT /*é中*/ missing_input", "4", "15"},
		{"utf8_bound_query", "DECLARE\n c CURSOR FOR\n SELECT /*é中*/ missing_input;\nBEGIN\n OPEN\n c;\nEND", "SELECT /*é中*/ missing_input", "5", "15"},
		{"utf8_using", "DECLARE s text:='é中'; c refcursor;\nBEGIN\n OPEN c FOR EXECUTE 'SELECT $1::text,$2::integer' USING s,\n /* lead */ (missing_input /* interior */ + 1) /* tail */;\nEND", "(missing_input /* interior */ + 1)", "3", "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorFunction(t, p, b, c, "pc_origin", "", "void", tc.body)
				want := sqlCursorDiagnostic("42703", `column "missing_input" does not exist`)
				want.InternalQuery, want.Context = tc.internal, "PL/pgSQL function pc_origin() line "+tc.line+" at OPEN"
				want.InternalPosition = tc.position
				plCursorError(t, p, b, c, "SELECT pc_origin() AS v", want, true, false)
			})
		})
	}
	for _, tc := range []struct {
		name, declaration, actual, code, message, internal, position string
		current                                                      int64
	}{
		{"whole_projection_bind_before_side_effect", "c CURSOR(a bigint,b integer) FOR SELECT a+b;", "nextval('pc_origin_seq'),missing_input", "42703", `column "missing_input" does not exist`, "nextval('pc_origin_seq'), missing_input", "27", 10},
		{"whole_projection_evaluate_before_conversion", "c CURSOR(a integer,b bigint) FOR SELECT a+b;", "'bad',nextval('pc_origin_seq')", "22P02", `invalid input syntax for type integer: "bad"`, "", "", 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorStep(t, p, b, c, fixture.Exec("CREATE SEQUENCE pc_origin_seq START 10", 0))
				plCursorFunction(t, p, b, c, "pc_origin", "", "void", "DECLARE "+tc.declaration+" BEGIN OPEN c("+tc.actual+"); END")
				want := plCursorRuntime(tc.code, tc.message, "pc_origin()", "OPEN")
				want.InternalQuery = tc.internal
				want.InternalPosition = tc.position
				for _, prepared := range []bool{false, true} {
					sqlCursorStep(t, p, b, c, fixture.Select("SELECT setval('pc_origin_seq',10,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}))
					step := fixture.ErrorDiagnostic("SELECT pc_origin() AS v", want)
					step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
					sqlCursorStep(t, p, b, c, step)
					plCursorValue(t, p, b, c, "SELECT currval('pc_origin_seq') AS v", "INT8", tc.current)
				}
			})
		})
	}
	for _, tc := range []struct {
		name, result, body, metadata string
		want                         any
	}{
		{"whole_projection_first_row", "integer", "DECLARE c CURSOR(a integer) FOR SELECT a; x integer; BEGIN OPEN c(generate_series(1,3)); FETCH c INTO x; CLOSE c; RETURN x; END", "INT4", int64(1)},
		{"whole_projection_empty_row_found", "boolean", "DECLARE c CURSOR(a integer) FOR SELECT a; x integer; seen boolean; BEGIN OPEN c(generate_series(1,0)); seen:=FOUND; FETCH c INTO x; CLOSE c; RETURN NOT seen AND x IS NULL; END", "BOOL", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorFunction(t, p, b, c, "pc_origin", "", tc.result, tc.body)
				plCursorValue(t, p, b, c, "SELECT pc_origin() AS v", tc.metadata, tc.want)
			})
		})
	}
}
