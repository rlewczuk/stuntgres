package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLExceptionCallDiagnostics(t *testing.T) {
	for _, route := range []string{"called_pl", "sql_noninline_wrapper"} {
		for _, site := range []string{"assignment", "partial_assignment", "RETURN", "initializer"} {
			t.Run(route+"/"+site, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c,
						"CREATE FUNCTION ex_diag_called(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 12/x; EXCEPTION WHEN OTHERS THEN RAISE; END$$",
						"CREATE FUNCTION ex_diag_noninline() RETURNS integer LANGUAGE SQL AS $$SELECT 1; SELECT ex_diag_called(0)$$")
					callee := "ex_diag_called(0)"
					if route == "sql_noninline_wrapper" {
						callee = "ex_diag_noninline()"
					}
					body := "DECLARE x integer; BEGIN x := " + callee + "; RETURN x; END"
					if site == "partial_assignment" {
						body = "DECLARE x integer[] := ARRAY[1]; BEGIN x[1] := " + callee + "; RETURN x[1]; END"
					} else if site == "RETURN" {
						body = "BEGIN RETURN " + callee + "; END"
					} else if site == "initializer" {
						body = "DECLARE x integer := " + callee + "; BEGIN RETURN x; END"
					}
					plpgsqlExceptionDefine(t, p, b, c, "ex_diag_middle", "integer", body)
					// Pinned PostgreSQL 18.6 records include the real SQL
					// statement boundary and leave all other fields absent.
					want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
					action := "at " + site
					if site == "partial_assignment" {
						action = "at assignment"
					}
					if site == "initializer" {
						action = "during statement block local variable initialization"
					}
					want.Context = "PL/pgSQL function ex_diag_called(integer) line 1 at RETURN\n"
					if route == "sql_noninline_wrapper" {
						want.Context += "SQL function \"ex_diag_noninline\" statement 2\n"
					}
					want.Context += "PL/pgSQL function ex_diag_middle() line 1 " + action
					plpgsqlError(t, p, c, "SELECT ex_diag_middle() AS v", true, want)
				})
			})
		}
	}
}
