package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreRecordClear(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		value      int64
	}{
		{"unassigned_null", "DECLARE r record; BEGIN r := NULL; RETURN 7; END", 7},
		{"unassigned_typed_null", "DECLARE r record; BEGIN r := NULL::integer; RETURN 7; END", 7},
		{"clear_reassign_shape", "DECLARE r record; BEGIN r := ROW(1); r := NULL; r := ROW('second'::text,42); RETURN r.f2; END", 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_record_clear() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				plpgsqlValue(t, p, b, c, "SELECT pl_record_clear() AS v", "INT4", tc.value)
			})
		})
	}
	for _, tc := range []struct{ name, body, code, message, detail, expression, action string }{
		{"still_unassigned", "DECLARE r record; BEGIN r := NULL; RETURN r.a; END", "55000", `record "r" is not assigned yet`, "The tuple structure of a not-yet-assigned record is indeterminate.", "r.a", "RETURN"},
		{"clear_anonymous_shape", "DECLARE r record; BEGIN r := ROW(7); r := NULL; RETURN r.f1; END", "55000", `record "r" is not assigned yet`, "The tuple structure of a not-yet-assigned record is indeterminate.", "r.f1", "RETURN"},
		{"clear_named_shape", "DECLARE r record; BEGIN r := ROW(7)::pl_clear_pair; r := NULL; RETURN r.a; END", "55000", `record "r" is not assigned yet`, "The tuple structure of a not-yet-assigned record is indeterminate.", "r.a", "RETURN"},
		{"cleared_missing_field", "DECLARE r record; BEGIN r := ROW(1); r := NULL; RETURN r.missing; END", "55000", `record "r" is not assigned yet`, "The tuple structure of a not-yet-assigned record is indeterminate.", "r.missing", "RETURN"},
		{"non_null_scalar", "DECLARE r record; BEGIN r := ROW(1); r := NULL; r := 1; RETURN 7; END", "0A000", "input of anonymous composite types is not implemented", "", "", "assignment"},
		{"not_null_clear", "DECLARE r record NOT NULL := ROW(1); BEGIN r := NULL; RETURN 7; END", "22004", `null value cannot be assigned to variable "r" declared NOT NULL`, "", "", "assignment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TYPE pl_clear_pair AS (a integer)", "CREATE FUNCTION pl_record_clear_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$",
					"CREATE FUNCTION pl_record_clear_recovery() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN r := NULL; r := ROW(7); RETURN r.f1; END$$")
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pl_record_clear_error()", tc.action)
				want.Detail = tc.detail
				if tc.expression != "" {
					want.Context = "PL/pgSQL expression \"" + tc.expression + "\"\n" + want.Context
				}
				plpgsqlError(t, p, c, "SELECT pl_record_clear_error()", true, want)
				plpgsqlValue(t, p, b, c, "SELECT pl_record_clear_recovery() AS v", "INT4", int64(7))
			})
		})
	}
}
