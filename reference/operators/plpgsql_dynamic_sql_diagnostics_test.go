package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLDynamicSQLDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, command, code, message, token string }{
		{"syntax", "SELEC 1", "42601", "syntax error at or near \"SELEC\"", "SELEC"},
		{"column_utf8", "SELECT /* é中 */ absent_column", "42703", "column \"absent_column\" does not exist", "absent_column"},
		{"relation", "SELECT * FROM ds_absent_relation", "42P01", "relation \"ds_absent_relation\" does not exist", "ds_absent_relation"},
		{"execution", "SELECT 1/$1::integer", "22012", "division by zero", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION ds_diag(reached boolean) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN\n IF reached THEN EXECUTE '"+tc.command+"' INTO x USING 0; END IF; RETURN coalesce(x,5); END$$")
				plpgsqlValue(t, p, b, c, "SELECT ds_diag(false) AS v", "INT4", int64(5))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Context = "PL/pgSQL function ds_diag(boolean) line 2 at EXECUTE"
				if tc.token != "" {
					d.InternalQuery = tc.command
					d.InternalPosition = strconv.Itoa(utf8.RuneCountInString(tc.command[:strings.Index(tc.command, tc.token)]) + 1)
				} else {
					d.Context = "SQL statement \"" + tc.command + "\"\n" + d.Context
				}
				plpgsqlError(t, p, c, "SELECT ds_diag(true)", true, d)
			})
		})
	}
	for _, tc := range []struct{ name, statement, code, message string }{
		{"null", "EXECUTE NULL::text USING nextval('ds_diag_seq')", "22004", "query string argument of EXECUTE is null"},
		{"strict_zero", "EXECUTE 'SELECT 1 WHERE false' INTO STRICT x", "P0002", "query returned no rows"},
		{"strict_many", "EXECUTE 'SELECT i FROM generate_series(1,3) g(i)' INTO STRICT x", "P0003", "query returned more than one row"},
		{"target_conversion", "EXECUTE 'SELECT ''bad''::text' INTO x", "22P02", "invalid input syntax for type integer: \"bad\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE SEQUENCE ds_diag_seq", "CREATE FUNCTION ds_phase() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN "+tc.statement+"; RETURN x; END$$")
				plpgsqlError(t, p, c, "SELECT ds_phase()", true, plpgsqlRuntimeDiagnostic(tc.code, tc.message, "ds_phase()", "EXECUTE"))
				if tc.name == "null" {
					plpgsqlValue(t, p, b, c, "SELECT is_called AS v FROM ds_diag_seq", "BOOL", false)
				}
			})
		})
	}
	t.Run("using_before_sql_parse", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE SEQUENCE ds_phase_seq",
				"CREATE FUNCTION ds_phase_order() RETURNS bigint LANGUAGE plpgsql AS $$BEGIN BEGIN EXECUTE 'SELEC 1' USING nextval('ds_phase_seq'),nextval('ds_phase_seq'); EXCEPTION WHEN syntax_error THEN END; RETURN currval('ds_phase_seq'); END$$")
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE ds_phase_seq RESTART WITH 1", 0))
				s := fixture.Select("SELECT ds_phase_order() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		})
	})
	t.Run("expression_failure_before_computed_sql", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE SEQUENCE ds_operand_seq",
				"CREATE FUNCTION ds_command_error(n integer) RETURNS void LANGUAGE plpgsql AS $$BEGIN EXECUTE (1/n)::text USING nextval('ds_operand_seq'); END$$",
				"CREATE FUNCTION ds_using_error(n integer) RETURNS void LANGUAGE plpgsql AS $$BEGIN EXECUTE 'SELEC 1' USING 1/n,nextval('ds_operand_seq'); END$$")
			for _, name := range []string{"ds_command_error", "ds_using_error"} {
				plpgsqlError(t, p, c, "SELECT "+name+"(0)", true, plpgsqlRuntimeDiagnostic("22012", "division by zero", name+"(integer)", "EXECUTE"))
				plpgsqlValue(t, p, b, c, "SELECT is_called AS v FROM ds_operand_seq", "BOOL", false)
			}
		})
	})
	t.Run("duplicate_clauses_and_scalar_set_compile", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ statement, token, message string }{
				{"EXECUTE 'SELECT 1' INTO x INTO x;", "INTO x;", "syntax error at or near \"INTO\""},
				{"EXECUTE 'SELECT 1' USING 1 USING 2;", "2;", "syntax error at or near \"2\""},
				{"RETURN QUERY EXECUTE 'SELECT 1';", "RETURN QUERY", "cannot use RETURN QUERY in a non-SETOF function"},
			} {
				q := "CREATE FUNCTION ds_invalid() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN " + tc.statement + " RETURN x; END$$"
				d := fixture.ControlsReferenceDiagnostic("42601", tc.message)
				if strings.HasPrefix(tc.statement, "RETURN QUERY") {
					d.Code = "42804"
				}
				d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
				plpgsqlError(t, p, c, q, false, d)
			}
		})
	})
}
