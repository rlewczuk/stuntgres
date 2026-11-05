package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Exact diagnostics are independently grounded in the pinned PG18.6 oracle.
func TestReferenceBackendStateDiagnostics(t *testing.T) {
	type diagnosticCase struct{ q, token, code, message, hint string }
	var cases []diagnosticCase
	for i, f := range stateFunctions {
		for _, call := range []struct{ args, signature string }{{"", ""}, {"1,2", "integer, integer"}, {"true", "boolean"}, {"'1'::text", "text"}, {"1::bigint", "bigint"}, {"backendid => 1", "backendid => integer"}, {"pid => 1", "pid => integer"}} {
			cases = append(cases, diagnosticCase{"SELECT " + f + "(" + call.args + ")", f, "42883", "function " + f + "(" + call.signature + ") does not exist", identityNoMatchHint})
		}
		if i != 5 {
			cases = append(cases, diagnosticCase{"SELECT " + f + "(bid => 1)", f, "42883", "function " + f + "(bid => integer) does not exist", identityNoMatchHint})
		}
		cases = append(cases,
			diagnosticCase{"SELECT " + f + "(bid => 1,bid => 2)", "bid => 2", "42601", `argument name "bid" used more than once`, ""},
			diagnosticCase{"SELECT " + f + "(bid => 1,2)", "2)", "42601", "positional argument cannot follow named argument", ""},
			diagnosticCase{"SELECT " + f + "(*)", f, "42883", "function " + f + "() does not exist", identityNoMatchHint},
			diagnosticCase{"SELECT " + f + "('bad')", "'bad'", "22P02", `invalid input syntax for type integer: "bad"`, ""},
			diagnosticCase{"SELECT " + f + "('2147483648')", "'2147483648'", "22003", `value "2147483648" is out of range for type integer`, ""},
		)
		message := `a column definition list is only allowed for functions returning "record"`
		if i == 5 {
			message = "a column definition list is redundant for a function with OUT parameters"
		}
		cases = append(cases, diagnosticCase{"SELECT * FROM " + f + "(0) AS f(value integer)", "value integer", "42601", message, ""})
		for _, decoration := range []struct{ suffix, message string }{{"(DISTINCT 1)", "DISTINCT specified, but " + f + " is not an aggregate function"}, {"(1 ORDER BY 1)", "ORDER BY specified, but " + f + " is not an aggregate function"}, {"(1) FILTER (WHERE true)", "FILTER specified, but " + f + " is not an aggregate function"}, {"(1) OVER ()", "OVER specified, but " + f + " is not a window function nor an aggregate function"}} {
			cases = append(cases, diagnosticCase{"SELECT " + f + decoration.suffix, f, "42809", decoration.message, ""})
		}
	}
	cases = append(cases,
		diagnosticCase{"SELECT pg_catalog.pg_stat_get_backend_state_missing(1)", "pg_catalog.pg_stat_get_backend_state_missing", "42883", "function pg_catalog.pg_stat_get_backend_state_missing(integer) does not exist", identityNoMatchHint},
		diagnosticCase{"SELECT (pg_stat_get_backend_subxact(1)).subxact_overflow", "pg_stat_get_backend_subxact", "42703", `could not identify column "subxact_overflow" in record data type`, ""},
	)
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		for n, tc := range cases {
			t.Run(fmt.Sprint(n), func(t *testing.T) {
				for _, prefix := range []string{"", "/*é🙂*/ "} {
					for _, prepared := range []bool{false, true} {
						q := prefix + tc.q
						step := fixture.ErrorDiagnostic(q, identityDiagnostic(q, tc.token, tc.code, tc.message, tc.hint))
						step.SelectRows = true
						step.Prepared = prepared
						step.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
						fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
					}
				}
			})
		}
		stateBoth(t, p, b, o, fixture.Select("SELECT 42 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(42)}}))
		stateBoth(t, p, b, o, fixture.Select("SELECT subxact_count,subxact_overflowed FROM pg_stat_get_backend_subxact(bid => -1)", stateSubNames, stateSubTypes, [][]any{{nil, nil}}))
	})
}
