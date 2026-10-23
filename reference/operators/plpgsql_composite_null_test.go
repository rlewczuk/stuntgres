package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreCompositeNull(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		value      bool
	}{
		{"uninitialized", "DECLARE r pl_null_pair; BEGIN RETURN r IS DISTINCT FROM NULL; END", false},
		{"typed_null", "DECLARE r pl_null_pair := NULL::pl_null_pair; BEGIN RETURN r IS DISTINCT FROM NULL; END", false},
		{"clear_populated", "DECLARE r pl_null_pair := ROW(7); BEGIN r := NULL; RETURN r IS DISTINCT FROM NULL; END", false},
		{"row_null", "DECLARE r pl_null_pair := ROW(NULL::integer); BEGIN RETURN r IS NULL AND r IS DISTINCT FROM NULL; END", true},
		{"null_field", "DECLARE r pl_null_pair; BEGIN RETURN r.a IS NULL; END", true},
		{"field_materializes", "DECLARE r pl_null_pair; BEGIN r.a := 7; RETURN r.a=7 AND r IS DISTINCT FROM NULL; END", true},
		{"cleared_field_materializes", "DECLARE r pl_null_pair := ROW(1); BEGIN r := NULL; r.a := 7; RETURN r.a=7 AND r IS DISTINCT FROM NULL; END", true},
		{"not_null_row_null", "DECLARE r pl_null_pair NOT NULL := ROW(NULL::integer); BEGIN RETURN r IS NULL AND r IS DISTINCT FROM NULL; END", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TYPE pl_null_pair AS (a integer)", "CREATE FUNCTION pl_null_row() RETURNS boolean LANGUAGE plpgsql AS $$"+tc.body+"$$")
				plpgsqlValue(t, p, b, c, "SELECT pl_null_row() AS v", "BOOL", tc.value)
			})
		})
	}
	for _, tc := range []struct{ name, body, action string }{
		{"not_null_initializer", "DECLARE r pl_null_pair NOT NULL := NULL; BEGIN RETURN 1; END", "statement block local variable initialization"},
		{"not_null_assignment", "DECLARE r pl_null_pair NOT NULL := ROW(1); BEGIN r := NULL; RETURN 1; END", "assignment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TYPE pl_null_pair AS (a integer)", "CREATE FUNCTION pl_null_row_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$",
					"CREATE FUNCTION pl_null_row_recovery() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r pl_null_pair; BEGIN r.a := 7; RETURN r.a; END$$")
				want := plpgsqlRuntimeDiagnostic("22004", `null value cannot be assigned to variable "r" declared NOT NULL`, "pl_null_row_error()", tc.action)
				plpgsqlError(t, p, c, "SELECT pl_null_row_error()", true, want)
				plpgsqlValue(t, p, b, c, "SELECT pl_null_row_recovery() AS v", "INT4", int64(7))
			})
		})
	}
}
