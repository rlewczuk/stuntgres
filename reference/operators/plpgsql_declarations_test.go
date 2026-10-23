package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreDeclarations(t *testing.T) {
	plpgsqlSelectedCast(t)
	for _, tc := range []struct {
		name, body, typ string
		value           any
	}{
		{"null", "DECLARE x integer; BEGIN RETURN x IS NULL; END", "BOOL", true},
		{"type_copy", "DECLARE x CONSTANT integer NOT NULL := 7; y x%TYPE; BEGIN y := NULL; RETURN y IS NULL; END", "BOOL", true},
		{"forms", "DECLARE x integer = 1; y integer DEFAULT x+1; z integer := y+1; BEGIN z = z+1; RETURN z; END", "INT4", int64(4)},
		{"io", "DECLARE s text := '42'; n integer; BEGIN n := s; RETURN n; END", "INT4", int64(42)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				result := "integer"
				if tc.typ == "BOOL" {
					result = "boolean"
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_decl() RETURNS "+result+" LANGUAGE plpgsql AS $$"+tc.body+"$$")
				plpgsqlValue(t, p, b, c, "SELECT pl_decl() AS v", tc.typ, tc.value)
			})
		})
	}
	for _, tc := range []struct {
		name, body, code, message, action string
		compile                           bool
	}{
		{"constant", "DECLARE x CONSTANT integer := 1; BEGIN x := 2; RETURN x; END", "22005", `variable "x" is declared CONSTANT`, "", true},
		{"constant_alias", "DECLARE x CONSTANT integer := 1; a ALIAS FOR x; BEGIN a := 2; RETURN x; END", "22005", `variable "x" is declared CONSTANT`, "", true},
		{"nn_missing", "DECLARE x integer NOT NULL; BEGIN RETURN x; END", "22004", `variable "x" must have a default value, since it's declared NOT NULL`, "", true},
		{"nn_init", "DECLARE x integer NOT NULL := NULL; BEGIN RETURN x; END", "22004", `null value cannot be assigned to variable "x" declared NOT NULL`, "statement block local variable initialization", false},
		{"nn_write", "DECLARE x integer NOT NULL := 1; BEGIN x := NULL; RETURN x; END", "22004", `null value cannot be assigned to variable "x" declared NOT NULL`, "assignment", false},
		{"bad_io", "DECLARE s text := 'bad'; n integer; BEGIN n := s; RETURN n; END", "22P02", `invalid input syntax for type integer: "bad"`, "assignment", false},
		{"typmod", "DECLARE x varchar(3); y x%TYPE := 'abcd'; BEGIN RETURN 1; END", "22001", "value too long for type character varying(3)", "statement block local variable initialization", false},
		{"missing_alias", "DECLARE a ALIAS FOR x; BEGIN RETURN 1; END", "42704", `variable "x" does not exist`, "", true},
		{"missing_type", "DECLARE a x%TYPE; BEGIN RETURN 1; END", "42704", `variable "x" does not exist`, "", true},
		{"duplicate", "DECLARE x integer; x integer; BEGIN RETURN 1; END", "42601", `duplicate declaration at or near "x"`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "CREATE FUNCTION pl_decl_error() RETURNS integer LANGUAGE plpgsql AS $$" + tc.body + "$$"
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.compile {
					want.Position = map[string]string{
						"constant": "110", "constant_alias": "125", "nn_missing": "89",
						"missing_alias": "91", "duplicate": "90",
					}[tc.name]
					if tc.name == "missing_type" {
						want.Context = `compilation of PL/pgSQL function "pl_decl_error" near line 1`
					}
					plpgsqlError(t, p, c, q, false, want)
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='pl_decl_error'", "INT8", int64(0))
				} else {
					userOperatorExec(t, p, b, c, q)
					want = plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pl_decl_error()", tc.action)
					plpgsqlError(t, p, c, "SELECT pl_decl_error()", true, want)
				}
				plpgsqlValue(t, p, b, c, "SELECT 9 AS v", "INT4", int64(9))
			})
		})
	}
	t.Run("column_type", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE pl_type_source(x varchar(3) NOT NULL DEFAULT 'abc' CHECK(x <> 'bad'))",
				"CREATE FUNCTION pl_column_type() RETURNS boolean LANGUAGE plpgsql AS $$DECLARE x pl_type_source.x%TYPE; BEGIN RETURN x IS NULL; END$$")
			plpgsqlValue(t, p, b, c, "SELECT pl_column_type() AS v", "BOOL", true)
		})
	})
}
