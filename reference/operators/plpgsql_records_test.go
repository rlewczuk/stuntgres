package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreRowsRecords(t *testing.T) {
	for _, tc := range []struct {
		name, body, typ string
		value           any
	}{
		{"known_null", "DECLARE r pl_row_source%ROWTYPE; BEGIN RETURN r.a IS NULL AND r.b IS NULL; END", "BOOL", true},
		{"fields", "DECLARE r pl_row_source%ROWTYPE; BEGIN r.a := 42; r.b := 'bad'; RETURN r.a; END", "INT4", int64(42)},
		{"missing_null", "DECLARE r pl_row_source%ROWTYPE; BEGIN r := ROW(7); RETURN r.a=7 AND r.b IS NULL; END", "BOOL", true},
		{"extra_ignored", "DECLARE r pl_row_one; BEGIN r := ROW(7,'ignored'); RETURN r.a; END", "INT4", int64(7)},
		{"field_io", "DECLARE r pl_row_one; BEGIN r := ROW('42'::text); RETURN r.a; END", "INT4", int64(42)},
		{"ordinal", "DECLARE r pl_row_one; s pl_row_other := ROW(8); BEGIN r := s; RETURN r.a; END", "INT4", int64(8)},
		{"record_shape", "DECLARE r record; BEGIN r := ROW(7,'first'); r.f1 := 9; r := ROW('second'::text,42,true); RETURN r.f2; END", "INT4", int64(42)},
		{"record_null", "DECLARE r record; BEGIN r := ROW(NULL::integer); RETURN r.f1 IS NULL; END", "BOOL", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE pl_row_source(a integer NOT NULL DEFAULT 99,b text CHECK(b <> 'bad'))",
					"CREATE TYPE pl_row_one AS (a integer)", "CREATE TYPE pl_row_other AS (different integer)")
				result := "integer"
				if tc.typ == "BOOL" {
					result = "boolean"
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_rows() RETURNS "+result+" LANGUAGE plpgsql AS $$"+tc.body+"$$")
				plpgsqlValue(t, p, b, c, "SELECT pl_rows() AS v", tc.typ, tc.value)
			})
		})
	}
	for _, tc := range []struct{ name, body, code, message, detail string }{
		{"unassigned", "DECLARE r record; BEGIN RETURN r.a; END", "55000", `record "r" is not assigned yet`, "The tuple structure of a not-yet-assigned record is indeterminate."},
		{"missing", "DECLARE r record; BEGIN r := ROW(1); RETURN r.missing; END", "42703", `record "r" has no field "missing"`, ""},
		{"scalar", "DECLARE r record; BEGIN r := 1; RETURN 1; END", "0A000", "input of anonymous composite types is not implemented", ""},
		{"bad_field", "DECLARE r pl_row_bad; BEGIN r := ROW('bad'::text); RETURN 1; END", "22P02", `invalid input syntax for type integer: "bad"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TYPE pl_row_bad AS (a integer)", "CREATE FUNCTION pl_row_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				action := "RETURN"
				if tc.name == "scalar" || tc.name == "bad_field" {
					action = "assignment"
				}
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pl_row_error()", action)
				want.Detail = tc.detail
				if tc.name == "unassigned" {
					want.Context = "PL/pgSQL expression \"r.a\"\n" + want.Context
				}
				if tc.name == "missing" {
					want.Context = "PL/pgSQL expression \"r.missing\"\n" + want.Context
				}
				plpgsqlError(t, p, c, "SELECT pl_row_error()", true, want)
				plpgsqlValue(t, p, b, c, "SELECT 1 AS v", "INT4", int64(1))
			})
		})
	}
}
