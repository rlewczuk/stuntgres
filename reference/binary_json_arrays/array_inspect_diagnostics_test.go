package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayInspectDiagnostics(t *testing.T) {
	cases := []struct{ call, token, code, message, hint, detail string }{
		{"array_dims(1)", "array_dims", "42883", "function array_dims(integer) does not exist", arrayInspectHint, ""},
		{"cardinality('x'::text)", "cardinality", "42883", "function cardinality(text) does not exist", arrayInspectHint, ""},
		{"array_length('{1}'::int[],1::bigint)", "array_length", "42883", "function array_length(integer[], bigint) does not exist", arrayInspectHint, ""},
		{"array_length('{1}'::int[],1::numeric)", "array_length", "42883", "function array_length(integer[], numeric) does not exist", arrayInspectHint, ""},
		{"array_lower('{1}'::int[],'1'::text)", "array_lower", "42883", "function array_lower(integer[], text) does not exist", arrayInspectHint, ""},
		{"array_length()", "array_length", "42883", "function array_length() does not exist", arrayInspectHint, ""},
		{"array_ndims('{1}'::int[],1)", "array_ndims", "42883", "function array_ndims(integer[], integer) does not exist", arrayInspectHint, ""},
		{"public.array_dims('{1}'::int[])", "public.array_dims", "42883", "function public.array_dims(integer[]) does not exist", arrayInspectHint, ""},
		{`"ARRAY_DIMS"('{1}'::int[])`, `"ARRAY_DIMS"`, "42883", "function ARRAY_DIMS(integer[]) does not exist", arrayInspectHint, ""},
		{"array_dims(a => '{1}'::int[])", "array_dims", "42883", "function array_dims(a => integer[]) does not exist", arrayInspectHint, ""},
		{"cardinality(NULL)", "", "42804", "could not determine polymorphic type because input has type unknown", "", ""},
		{"array_dims('{1}')", "", "42804", "could not determine polymorphic type because input has type unknown", "", ""},
		{"array_length(NULL,'bad')", "", "42804", "could not determine polymorphic type because input has type unknown", "", ""},
		{"array_length(NULL,1::bigint)", "array_length", "42883", "function array_length(unknown, bigint) does not exist", arrayInspectHint, ""},
		{"array_length('{1}'::int[], 'bad')", "'bad'", "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
		{"array_length(NULL::int[], 'bad')", "'bad'", "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
		{"array_upper('{1}'::int[], '2147483648'::int)", "'2147483648'", "22003", `value "2147483648" is out of range for type integer`, "", ""},
		{"cardinality('{1,,2}'::int[])", "'{1,,2}'", "22P02", `malformed array literal: "{1,,2}"`, "", `Unexpected "," character.`},
		{"array_dims(DISTINCT ARRAY[1])", "array_dims", "42809", "DISTINCT specified, but array_dims is not an aggregate function", "", ""},
		{"pg_catalog.array_dims(DISTINCT ARRAY[1])", "pg_catalog.array_dims", "42809", "DISTINCT specified, but pg_catalog.array_dims is not an aggregate function", "", ""},
		{"array_dims(ARRAY[1] ORDER BY 1)", "array_dims", "42809", "ORDER BY specified, but array_dims is not an aggregate function", "", ""},
		{"cardinality(ARRAY[1]) OVER()", "cardinality", "42809", "OVER specified, but cardinality is not a window function nor an aggregate function", "", ""},
		{"array_length(NULL,1) FILTER(WHERE true)", "array_length", "42809", "FILTER specified, but array_length is not an aggregate function", "", ""},
		{"array_length(ARRAY[1],'bad') FILTER(WHERE true)", "array_length", "42809", "FILTER specified, but array_length is not an aggregate function", "", ""},
		{"array_length(*)", "array_length", "42883", "function array_length() does not exist", arrayInspectHint, ""},
		{"array_dims(" + strings.Repeat("1,", 100) + "1)", "array_dims", "54023", "cannot pass more than 100 arguments to a function", "", ""},
		{"array_dims(missing)", "missing", "42703", `column "missing" does not exist`, "", ""},
		{"array_dims(" + strings.Repeat("1,", 100) + "missing)", "missing", "42703", `column "missing" does not exist`, "", ""},
		{"array_dims(1) FILTER(WHERE missing)", "missing", "42703", `column "missing" does not exist`, "", ""},
	}
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range cases {
			t.Run(tc.call, func(t *testing.T) {
				for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
					q := prefix + tc.call
					d := arrayInspectDiagnostic(q, tc.token, tc.code, tc.message, tc.hint)
					d.Detail = tc.detail
					arrayInspectDiagnosticBoth(t, p, b, c, q, d)
				}
			})
		}
		for _, input := range []any{"{1}", int64(1), nil} {
			q := "SELECT cardinality($1)"
			arrayInspectDiagnosticBoth(t, p, b, c, q, arrayInspectDiagnostic(q, "", "42804", "could not determine polymorphic type because input has type unknown", ""), input)
		}
	})
}
