package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLAssignmentDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, assignment, code, message, detail, domain, constraint string
	}{
		{"bad_text", "a integer[] := '{1,2}'", "a[1] := 'bad'::text", "22P02", `invalid input syntax for type integer: "bad"`, "", "", ""},
		{"bad_text_array_element", "a integer[] := '{1,2}'", "a[1] := ARRAY['bad']::text[]", "22P02", `invalid input syntax for type integer: "{bad}"`, "", "", ""},
		{"scalar_slice", "a integer[] := '{1,2}'", "a[1:2] := 4", "22P02", `malformed array literal: "4"`, `Array value must start with "{" or dimension information.`, "", ""},
		{"null_bound", "a integer[] := '{1,2}'", "a[NULL::integer] := 7", "22004", "array subscript in assignment must not be null", "", "", ""},
		{"null_slice_bound", "a integer[] := '{1,2}'", "a[1:NULL::integer] := '{7}'::integer[]", "22004", "array subscript in assignment must not be null", "", "", ""},
		{"typed_text_bound", "a integer[] := '{1,2}'", "a['1'::text] := 7", "42804", "array subscript must have type integer", "", "", ""},
		{"non_array", "a integer := 1", "a[1] := 7", "42804", "cannot subscript type integer because it does not support subscripting", "", "", ""},
		{"wrong_rank", "a integer[] := '{{1,2},{3,4}}'", "a[1] := 7", "2202E", "wrong number of array subscripts", "", "", ""},
		{"multidimensional_extension", "a integer[] := '{{1,2},{3,4}}'", "a[3][1] := 7", "2202E", "array subscript out of range", "", "", ""},
		{"inverted_slice", "a integer[] := '{1,2}'", "a[2:1] := '{7}'::integer[]", "2202E", "upper bound cannot be less than lower bound", "", "", ""},
		{"short_source", "a integer[] := '{1,2}'", "a[1:2] := '{7}'::integer[]", "2202E", "source array too small", "", "", ""},
		{"null_omitted_lower", "a integer[]", "a[:2] := '{7,8}'::integer[]", "2202E", "array slice subscript must provide both boundaries", "When assigning to a slice of an empty array value, slice boundaries must be fully specified.", "", ""},
		{"empty_omitted_upper", "a integer[] := '{}'", "a[1:] := '{7,8}'::integer[]", "2202E", "array slice subscript must provide both boundaries", "When assigning to a slice of an empty array value, slice boundaries must be fully specified.", "", ""},
		{"varchar_typmod", "a varchar(3)[] := '{one,two}'", "a[1] := 'long'::text", "22001", "value too long for type character varying(3)", "", "", ""},
		{"domain_element_check", "a pa_positive[] := '{1,2}'", "a[1] := -1", "23514", `value for domain pa_positive violates check constraint "pa_positive_check"`, "", "pa_positive", "pa_positive_check"},
		{"domain_element_not_null", "a pa_required[] := '{1,2}'", "a[1] := NULL", "23502", "domain pa_required does not allow null values", "", "pa_required", ""},
		{"domain_container", "a pa_array := '{1,2}'", "a[1] := -1", "23514", `value for domain pa_array violates check constraint "pa_array_check"`, "", "pa_array", "pa_array_check"},
		{"domain_composite_container", "a pa_box_domain := ROW('{1,2}'::integer[],9)", "a.a[1] := -1", "23514", `value for domain pa_box_domain violates check constraint "pa_box_check"`, "", "pa_box_domain", "pa_box_check"},
		{"unassigned_record", "r record", "r.a[1] := 7", "55000", `record "r" is not assigned yet`, "The tuple structure of a not-yet-assigned record is indeterminate.", "", ""},
		{"missing_record_field", "r record := ROW('{1,2}'::integer[])", "r.absent[1] := 7", "42703", `record "r" has no field "absent"`, "", "", ""},
		{"multibyte_position", "a integer[] := '{1,2}'; é text := 'é'", "\n  a[ '1'::text ] := 7", "42804", "array subscript must have type integer", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE DOMAIN pa_positive AS integer CONSTRAINT pa_positive_check CHECK(VALUE>0)",
					"CREATE DOMAIN pa_required AS integer NOT NULL",
					"CREATE DOMAIN pa_array AS integer[] CONSTRAINT pa_array_check CHECK(VALUE[1]>0)",
					"CREATE TYPE pa_box AS (a integer[], sibling integer)",
					"CREATE DOMAIN pa_box_domain AS pa_box CONSTRAINT pa_box_check CHECK((VALUE).a[1]>0)",
					"CREATE FUNCTION pa_error() RETURNS integer LANGUAGE plpgsql AS $$DECLARE "+tc.declaration+"; BEGIN "+tc.assignment+"; RETURN 1; END$$")
				// Complete optional fields and execute phases observed on pinned
				// PostgreSQL 18.6; absent fields remain part of the contract.
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pa_error()", "assignment")
				want.Detail, want.DataType, want.Constraint = tc.detail, tc.domain, tc.constraint
				if tc.domain != "" {
					want.Schema = "public"
				}
				switch tc.name {
				case "bad_text", "varchar_typmod", "unassigned_record", "missing_record_field":
					want.Context = "PL/pgSQL assignment \"" + tc.assignment + "\"\n" + want.Context
				case "typed_text_bound":
					want.InternalPosition = "3"
					want.InternalQuery = "a['1'::text] := 7"
				case "non_array":
					want.InternalPosition = "1"
					want.InternalQuery = "a[1] := 7"
				case "multibyte_position":
					want.InternalPosition = "4"
					want.InternalQuery = "a[ '1'::text ] := 7"
					want.Context = "PL/pgSQL function pa_error() line 2 at assignment"
				}
				plpgsqlError(t, p, c, "SELECT pa_error()", true, want)
			})
		})
	}
	t.Run("selected_cast_failure_not_io_retry", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE pa_fail_source AS ENUM ('42')",
				"CREATE FUNCTION pa_fail_cast(pa_fail_source) RETURNS integer LANGUAGE SQL AS 'SELECT 1/0'")
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "DROP CAST IF EXISTS (pa_fail_source AS integer)"); err != nil {
					t.Error(err)
				}
			}()
			userOperatorExec(t, p, b, c,
				"CREATE CAST (pa_fail_source AS integer) WITH FUNCTION pa_fail_cast(pa_fail_source) AS ASSIGNMENT",
				"CREATE FUNCTION pa_fail() RETURNS integer[] LANGUAGE plpgsql AS $$DECLARE a integer[] := '{1,2}'; BEGIN a[1] := '42'::pa_fail_source; RETURN a; END$$")
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "pa_fail()", "assignment")
			sqlContext := `SQL function "pa_fail_cast" statement 1`
			if b.PG {
				sqlContext = `SQL function "pa_fail_cast" during inlining`
			}
			want.Context = sqlContext + "\nPL/pgSQL assignment \"a[1] := '42'::pa_fail_source\"\n" + want.Context
			plpgsqlError(t, p, c, "SELECT pa_fail()", true, want)
		})
	})
}
