package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const resetSharedHint = `Target must be "archiver", "bgwriter", "checkpointer", "io", "recovery_prefetch", "slru", or "wal".`

func TestReferenceStatsResetSharedDiagnostics(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, target := range []string{"", "WAL", "wal ", "a\"b", "a\nb"} {
			query := "SELECT pg_stat_reset_shared('" + target + "')"
			want := activityDiagnostic("22023", `unrecognized reset target: "`+target+`"`, 0)
			want.Hint = resetSharedHint
			resetReplicationRuntimeError(t, p, b, c, query, want)
			resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset_slru('"+target+"')", "pg_stat_reset_slru")
		}
		for _, f := range resetSharedFunctions {
			failures := []struct{ suffix, signature string }{{"(NULL,NULL)", "unknown, unknown"}, {"(true)", "boolean"}, {"(x => NULL)", "x => unknown"}}
			if f.strict {
				failures = append(failures, struct{ suffix, signature string }{"()", ""}, struct{ suffix, signature string }{"('bad'::text)", "text"}, struct{ suffix, signature string }{"(pid => 1)", "pid => integer"}, struct{ suffix, signature string }{"(target => 1)", "target => integer"})
			} else {
				failures = append(failures, struct{ suffix, signature string }{"(1)", "integer"})
			}
			for _, tc := range failures {
				want := activityDiagnostic("42883", "function "+f.name+"("+tc.signature+") does not exist", 8)
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t, p, c, "SELECT "+f.name+tc.suffix, want)
			}
			for _, call := range []struct{ call, signature string }{{"public." + f.name + "(NULL)", "public." + f.name + "(unknown)"}, {"\"" + strings.ToUpper(f.name) + "\"(NULL)", strings.ToUpper(f.name) + "(unknown)"}, {f.name + "_missing(NULL)", f.name + "_missing(unknown)"}} {
				want := activityDiagnostic("42883", "function "+call.signature+" does not exist", 8)
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t, p, c, "SELECT "+call.call, want)
			}
			want := activityDiagnostic("42883", "function "+f.name+"(boolean) does not exist", 14)
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			activityError(t, p, c, "/*é*/ SELECT "+f.name+"(true)", want)
			for _, decoration := range []struct{ suffix, message string }{
				{"(DISTINCT NULL::" + f.argument + ")", "DISTINCT specified, but " + f.name + " is not an aggregate function"},
				{"(NULL::" + f.argument + " ORDER BY 1)", "ORDER BY specified, but " + f.name + " is not an aggregate function"},
				{"(NULL::" + f.argument + ") FILTER (WHERE true)", "FILTER specified, but " + f.name + " is not an aggregate function"},
				{"(NULL::" + f.argument + ") OVER ()", "OVER specified, but " + f.name + " is not a window function nor an aggregate function"},
			} {
				activityError(t, p, c, "SELECT "+f.name+decoration.suffix, activityDiagnostic("42809", decoration.message, 8))
			}
			for _, syntax := range []struct{ args, token, message string }{{"x=>NULL,x=>NULL", "x=>NULL)", `argument name "x" used more than once`}, {"x=>NULL,NULL", "NULL)", "positional argument cannot follow named argument"}} {
				query := "SELECT " + f.name + "(" + syntax.args + ")"
				activityError(t, p, c, query, activityDiagnostic("42601", syntax.message, strings.Index(query, syntax.token)+1))
			}
			activityError(t, p, c, "SELECT reset_missing_schema."+f.name+"(NULL)", activityDiagnostic("3F000", `schema "reset_missing_schema" does not exist`, 8))
		}
	})
}
