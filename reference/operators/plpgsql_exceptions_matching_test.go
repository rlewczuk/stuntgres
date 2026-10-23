package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLExceptionMatching(t *testing.T) {
	for _, tc := range []struct {
		name, clauses string
		want          int64
	}{
		{"name", "WHEN division_by_zero THEN RETURN 1;", 1},
		{"uppercase", "WHEN DIVISION_BY_ZERO THEN RETURN 2;", 2},
		{"code", "WHEN SQLSTATE '22012' THEN RETURN 3;", 3},
		{"category_name", "WHEN data_exception THEN RETURN 4;", 4},
		{"category_code", "WHEN SQLSTATE '22000' THEN RETURN 5;", 5},
		{"or", "WHEN unique_violation OR division_by_zero THEN RETURN 6;", 6},
		{"ordered_category", "WHEN data_exception THEN RETURN 7; WHEN division_by_zero THEN RETURN 99;", 7},
		{"ordered_others", "WHEN OTHERS THEN RETURN 8; WHEN division_by_zero THEN RETURN 99;", 8},
		{"later", "WHEN unique_violation THEN RETURN 99; WHEN division_by_zero THEN RETURN 9;", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionDefine(t, p, b, c, "ex_match", "integer", "DECLARE x integer; BEGIN x := 1/0; RETURN 99; EXCEPTION "+tc.clauses+" END")
				plpgsqlValue(t, p, b, c, "SELECT ex_match() AS v", "INT4", tc.want)
			})
		})
	}
	t.Run("success_and_unused_code", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plpgsqlExceptionDefine(t, p, b, c, "ex_match", "integer", "BEGIN RETURN 7; EXCEPTION WHEN SQLSTATE 'ZX987' THEN RETURN 99; WHEN OTHERS THEN RETURN 98; END")
			plpgsqlValue(t, p, b, c, "SELECT ex_match() AS v", "INT4", int64(7))
			plpgsqlValue(t, p, b, c, `SELECT 3 AS v FROM (SELECT 1 AS exception, 2 AS raise) AS q`, "INT4", int64(3))
		})
	})
	for _, tc := range []struct{ name, condition, code string }{
		{"unknown", "ex_missing_condition", "42704"},
		{"warning", "warning", "42704"},
		{"success", "successful_completion", "42704"},
		{"short_code", "SQLSTATE '2201'", "42601"},
		{"lower_code", "SQLSTATE '22p02'", "42601"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "CREATE FUNCTION ex_bad() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 1; EXCEPTION WHEN " + tc.condition + " THEN RETURN 2; END$$"
				want := fixture.ControlsReferenceDiagnostic(tc.code, `unrecognized exception condition "`+tc.condition+`"`)
				want.Context = `compilation of PL/pgSQL function "ex_bad" near line 1`
				switch tc.name {
				case "short_code":
					want.Message = `invalid SQLSTATE code at or near "'2201'"`
					want.Position = "104"
					want.Context = ""
				case "lower_code":
					want.Message = `invalid SQLSTATE code at or near "'22p02'"`
					want.Position = "104"
					want.Context = ""
				}
				plpgsqlError(t, p, c, q, false, want)
				plpgsqlExceptionDefine(t, p, b, c, "ex_bad", "integer", "BEGIN RETURN 7; END")
				plpgsqlValue(t, p, b, c, "SELECT ex_bad() AS v", "INT4", int64(7))
			})
		})
	}
}

func TestReferencePLpgSQLExceptionRethrow(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"unmatched_inner", "DECLARE x integer; BEGIN BEGIN x := 1/0; EXCEPTION WHEN unique_violation THEN RETURN 99; END; RETURN 98; EXCEPTION WHEN division_by_zero THEN RETURN 7; END", 7},
		{"handler_error_bypasses", "DECLARE x integer; BEGIN BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN x := 'bad'::integer; WHEN OTHERS THEN RETURN 99; END; RETURN 98; EXCEPTION WHEN invalid_text_representation THEN RETURN 8; END", 8},
		{"nested_raise", "DECLARE x integer; BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN BEGIN RAISE; EXCEPTION WHEN division_by_zero THEN RETURN 9; END; END", 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionDefine(t, p, b, c, "ex_rethrow", "integer", tc.body)
				plpgsqlValue(t, p, b, c, "SELECT ex_rethrow() AS v", "INT4", tc.want)
			})
		})
	}
	for _, tc := range []struct{ name, body, code, message string }{
		{"original_restored", "DECLARE x integer; BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN BEGIN x := 'bad'::integer; EXCEPTION WHEN invalid_text_representation THEN x := 1; END; RAISE; END", "22012", "division by zero"},
		{"outside", "BEGIN RAISE; RETURN 99; END", "0Z002", "RAISE without parameters cannot be used outside an exception handler"},
		{"called_frame", "DECLARE x integer; BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN RETURN ex_bare(); END", "0Z002", "RAISE without parameters cannot be used outside an exception handler"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionDefine(t, p, b, c, "ex_bare", "integer", "BEGIN RAISE; RETURN 99; END")
				plpgsqlExceptionDefine(t, p, b, c, "ex_rethrow", "integer", tc.body)
				want := plpgsqlExceptionDivision("ex_rethrow()")
				switch tc.name {
				case "outside":
					want = plpgsqlRuntimeDiagnostic(tc.code, tc.message, "ex_rethrow()", "RAISE")
				case "called_frame":
					want = plpgsqlRuntimeDiagnostic(tc.code, tc.message, "ex_bare()", "RAISE")
					want.Context += "\nPL/pgSQL function ex_rethrow() line 1 at RETURN"
				}
				plpgsqlError(t, p, c, "SELECT ex_rethrow() AS v", true, want)
				plpgsqlValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
			})
		})
	}
}

func TestReferencePLpgSQLExceptionIntrinsics(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"values", "DECLARE x integer; BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN RETURN SQLSTATE || ':' || SQLERRM; END", "22012:division by zero"},
		{"nested_restore", `DECLARE x integer; s text; BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN s := SQLSTATE; BEGIN x := 'bad'::integer; EXCEPTION WHEN invalid_text_representation THEN s := s || ':' || SQLSTATE || ':' || SQLERRM; END; RETURN s || ':' || SQLSTATE || ':' || SQLERRM; END`, `22012:22P02:invalid input syntax for type integer: "bad":22012:division by zero`},
		{"shadow", "DECLARE sqlstate text := 'user'; x integer; BEGIN BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN x := length(sqlstate); END; RETURN sqlstate || ':' || x; END", "user:5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionDefine(t, p, b, c, "ex_intrinsic", "text", tc.body)
				plpgsqlValue(t, p, b, c, "SELECT ex_intrinsic() AS v", "TEXT", tc.want)
			})
		})
	}
	for _, variable := range []string{"SQLSTATE", "SQLERRM"} {
		t.Run("constant_"+variable, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "CREATE FUNCTION ex_constant() RETURNS text LANGUAGE plpgsql AS $$BEGIN RETURN 'ok'; EXCEPTION WHEN OTHERS THEN " + variable + " := 'changed'; RETURN 'bad'; END$$"
				want := fixture.ControlsReferenceDiagnostic("22005", `variable "`+strings.ToLower(variable)+`" is declared CONSTANT`)
				want.Position = "112"
				plpgsqlError(t, p, c, q, false, want)
			})
		})
	}
	for _, variable := range []string{"sqlstate", "sqlerrm"} {
		t.Run("outside_scope_"+variable, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionDefine(t, p, b, c, "ex_scope", "text", "BEGIN RETURN "+variable+"; EXCEPTION WHEN division_by_zero THEN RETURN "+variable+"; END")
				want := plpgsqlRuntimeDiagnostic("42703", `column "`+variable+`" does not exist`, "ex_scope()", "RETURN")
				want.InternalPosition = "1"
				want.InternalQuery = variable
				plpgsqlError(t, p, c, "SELECT ex_scope() AS v", true, want)
			})
		})
	}
}
