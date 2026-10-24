package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLQueryLoopsDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, declarations, source, code, message, token string
	}{
		{"missing_relation", "", "SELECT i FROM ql_missing", "42P01", `relation "ql_missing" does not exist`, "ql_missing"},
		{"missing_column", "", "SELECT missing_input FROM ql_diagnostic", "42703", `column "missing_input" does not exist`, "missing_input"},
		{"multibyte_comment", "", "SELECT /* é */ missing_input FROM ql_diagnostic", "42703", `column "missing_input" does not exist`, "missing_input"},
		{"ambiguous", "i integer:=7;", "SELECT i FROM ql_diagnostic", "42702", `column reference "i" is ambiguous`, "i"},
		{"source_error", "", "SELECT 10/(i-i) AS v FROM ql_diagnostic", "22012", "division by zero", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE ql_diagnostic(i integer)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ql_diagnostic VALUES(1)", 1))
				userOperatorExec(t, p, b, c, "CREATE FUNCTION ql_diagnostic_error() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; "+tc.declarations+" BEGIN FOR r IN "+tc.source+" LOOP END LOOP; RETURN 1; END$$")
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "ql_diagnostic_error()", "FOR over SELECT rows")
				if tc.token != "" {
					want.InternalQuery = tc.source
					want.InternalPosition = strconv.Itoa(utf8.RuneCountInString(tc.source[:strings.Index(tc.source, tc.token)]) + 1)
				}
				if tc.code == "42702" {
					want.Detail = "It could refer to either a PL/pgSQL variable or a table column."
				}
				plpgsqlError(t, p, c, "SELECT ql_diagnostic_error() AS v", true, want)
				userOperatorExec(t, p, b, c, "DROP FUNCTION ql_diagnostic_error()", "CREATE FUNCTION ql_diagnostic_error() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN SELECT 12 AS i LOOP END LOOP; RETURN r.i; END$$")
				plpgsqlValue(t, p, b, c, "SELECT ql_diagnostic_error() AS v", "INT4", int64(12))
			})
		})
	}
	t.Run("target_conversion_and_held_recovery", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE ql_convert_row(i integer,t text)",
				"CREATE FUNCTION ql_convert(x text) RETURNS integer LANGUAGE plpgsql AS $$DECLARE r ql_convert_row%ROWTYPE; BEGIN FOR r IN SELECT x,'ok'::text LOOP END LOOP; RETURN r.i; END$$")
			q := "SELECT ql_convert($1) AS v"
			want := plpgsqlRuntimeDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, "ql_convert(text)", "FOR over SELECT rows")
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want, "bad")
				s.SelectRows, s.ErrorStage, s.Prepared = true, "execute", prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			s := fixture.ErrorDiagnostic(q, want, "bad")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}, "12"))
			parametersValue(t, p, b, c, q, "v", "INT4", int64(12), "12")
		})
	})
	for _, prefix := range []string{"", "RETURN; "} {
		t.Run("undeclared_"+prefix, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "CREATE FUNCTION ql_bad_target() RETURNS void LANGUAGE plpgsql AS $$BEGIN " + prefix + "FOR absent IN SELECT 1 LOOP END LOOP; END$$"
				want := fixture.ControlsReferenceDiagnostic("42601", "loop variable of loop over rows must be a record variable or list of scalar variables")
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "absent IN")]) + 1)
				plpgsqlError(t, p, c, q, false, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='ql_bad_target'", "INT8", int64(0))
			})
		})
	}
	t.Run("later_case_ledger", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION ql_case() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN FOR r IN SELECT 7 AS a LOOP END LOOP; FOR r IN SELECT r.a AS i LOOP IF r.i=0 THEN RETURN 0; END IF; END LOOP; CASE r.i WHEN true THEN RETURN 1; ELSE RETURN 2; END CASE; END$$")
			want := plpgsqlRuntimeDiagnostic("42883", "operator does not exist: integer = boolean", "ql_case()", "CASE")
			want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			want.InternalQuery = `"__Case__Variable_4__" IN (true)`
			want.InternalPosition = "24"
			plpgsqlError(t, p, c, "SELECT ql_case() AS v", true, want)
		})
	})
}
