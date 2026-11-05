package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceBackendCurrentDiagnostics(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	type diagnosticCase struct{ q, token, code, message, hint string }
	cases := []diagnosticCase{
		{"SELECT pg_stat_get_activity()", "pg_stat_get_activity", "42883", "function pg_stat_get_activity() does not exist", hint},
		{"SELECT * FROM pg_stat_get_backend_io(true)", "pg_stat_get_backend_io", "42883", "function pg_stat_get_backend_io(boolean) does not exist", hint},
		{"SELECT pg_stat_get_backend_wal(1,2)", "pg_stat_get_backend_wal", "42883", "function pg_stat_get_backend_wal(integer, integer) does not exist", hint},
		{"SELECT pg_stat_get_activity(backend_pid => 1)", "pg_stat_get_activity", "42883", "function pg_stat_get_activity(backend_pid => integer) does not exist", hint},
		{"SELECT pg_stat_get_backend_io(pid => 1)", "pg_stat_get_backend_io", "42883", "function pg_stat_get_backend_io(pid => integer) does not exist", hint},
		{"SELECT pg_stat_get_activity('1'::text)", "pg_stat_get_activity", "42883", "function pg_stat_get_activity(text) does not exist", hint},
		{"SELECT pg_stat_get_backend_wal(1::bigint)", "pg_stat_get_backend_wal", "42883", "function pg_stat_get_backend_wal(bigint) does not exist", hint},
		{"SELECT pg_catalog.pg_stat_get_activity_missing(1)", "pg_catalog.pg_stat_get_activity_missing", "42883", "function pg_catalog.pg_stat_get_activity_missing(integer) does not exist", hint},
		{"SELECT pg_stat_get_activity(pid => 1,pid => 2)", "pid => 2", "42601", `argument name "pid" used more than once`, ""},
		{"SELECT pg_stat_get_backend_wal(backend_pid => 1,2)", "2)", "42601", "positional argument cannot follow named argument", ""},
	}
	for _, name := range backendFunctionNames {
		cases = append(cases, diagnosticCase{"SELECT * FROM " + name + "(0) AS f(value integer)", "value integer", "42601", "a column definition list is redundant for a function with OUT parameters", ""})
	}
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		for _, tc := range cases {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				for _, prepared := range []bool{false, true} {
					q := prefix + tc.q
					offset := strings.Index(q, tc.token)
					if offset < 0 {
						t.Fatal("missing diagnostic anchor")
					}
					d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint, Position: strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)}
					step := fixture.ErrorDiagnostic(q, d)
					step.SelectRows = true
					step.Prepared = prepared
					step.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
					fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
				}
			}
		}
		for _, q := range []string{"SELECT count(*) FROM pg_stat_get_activity(pid => 0::smallint)", "SELECT count(*) FROM pg_stat_get_backend_io(backend_pid => 0::smallint)", "SELECT count(wal_records) FROM pg_stat_get_backend_wal(backend_pid => 0::smallint)", "SELECT count(*) FROM pg_stat_get_activity(-2147483648)", "SELECT count(*) FROM pg_stat_get_backend_io(2147483647)"} {
			step := fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
			fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Prepared(step))
		}
	})
}
