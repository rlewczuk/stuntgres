package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

const identityNoMatchHint = "No function matches the given name and argument types. You might need to add explicit type casts."

// Exact diagnostics were independently checked against the pinned PG18.6 oracle.
func identityDiagnostic(q, token, code, message, hint string) fixture.Diagnostic {
	offset := strings.Index(q, token)
	if offset < 0 {
		panic("missing independent identity diagnostic token")
	}
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)}
}

func TestReferenceBackendIdentityDiagnostics(t *testing.T) {
	type diagnosticCase struct{ q, token, code, message, hint string }
	var cases []diagnosticCase
	for _, f := range identityFunctions {
		for _, call := range []struct{ args, signature string }{
			{"", ""}, {"1,2", "integer, integer"}, {"true", "boolean"}, {"'1'::text", "text"}, {"1::bigint", "bigint"},
			{"bid => 1", "bid => integer"}, {"backendid => 1", "backendid => integer"}, {"pid => 1", "pid => integer"}, {"backend_pid => 1", "backend_pid => integer"},
		} {
			cases = append(cases, diagnosticCase{"SELECT " + f + "(" + call.args + ")", f, "42883", "function " + f + "(" + call.signature + ") does not exist", identityNoMatchHint})
		}
		cases = append(cases,
			diagnosticCase{"SELECT " + f + "(bid => 1,bid => 2)", "bid => 2", "42601", `argument name "bid" used more than once`, ""},
			diagnosticCase{"SELECT " + f + "(bid => 1,2)", "2)", "42601", "positional argument cannot follow named argument", ""},
			diagnosticCase{"SELECT * FROM " + f + "(0) AS f(value integer)", "value integer", "42601", `a column definition list is only allowed for functions returning "record"`, ""},
			diagnosticCase{"SELECT " + f + "(*)", f, "42883", "function " + f + "() does not exist", identityNoMatchHint},
		)
	}
	for _, call := range []struct{ args, signature string }{{"1", "integer"}, {"bid => 1", "bid => integer"}, {"backendid => 1", "backendid => integer"}, {"pid => 1", "pid => integer"}, {"backend_pid => 1", "backend_pid => integer"}} {
		cases = append(cases, diagnosticCase{"SELECT pg_stat_get_backend_idset(" + call.args + ")", "pg_stat_get_backend_idset", "42883", "function pg_stat_get_backend_idset(" + call.signature + ") does not exist", identityNoMatchHint})
	}
	cases = append(cases, diagnosticCase{"SELECT pg_catalog.pg_stat_get_backend_identity_missing(1)", "pg_catalog.pg_stat_get_backend_identity_missing", "42883", "function pg_catalog.pg_stat_get_backend_identity_missing(integer) does not exist", identityNoMatchHint})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		for _, tc := range cases {
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
		}
		fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Select("SELECT 42 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(42)}}))
	})
}
