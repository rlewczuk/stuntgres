package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatsResetDatabaseDiagnostics(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsCreate(t, p, b, c, "reset_error_sentinel")
		statsPublish(t, p, b, "reset_error_sentinel", 2, fixture.Exec("INSERT INTO reset_error_sentinel VALUES(1),(2)", 2))
		activityExec(t, p, c, "SET stats_fetch_consistency=none")
		for _, f := range resetDatabaseFunctions {
			failures := []struct{ suffix, signature string }{{"(NULL,NULL)", "unknown, unknown"}, {"(true)", "boolean"}, {"('x'::text)", "text"}, {"(oid=>NULL)", "oid => unknown"}, {"(target=>NULL)", "target => unknown"}, {"(VARIADIC ARRAY[1])", "integer[]"}}
			if f.strict {
				failures = append(failures, struct{ suffix, signature string }{"()", ""})
			} else {
				failures = append(failures, struct{ suffix, signature string }{"(NULL)", "unknown"})
			}
			for _, tc := range failures {
				want := activityDiagnostic("42883", "function "+f.name+"("+tc.signature+") does not exist", 8)
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t, p, c, "SELECT "+f.name+tc.suffix, want)
			}
			if f.strict {
				want := activityDiagnostic("42883", "function "+f.name+"() does not exist", 8)
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t, p, c, "SELECT "+f.name+"(*)", want)
			} else {
				activityError(t, p, c, "SELECT "+f.name+"(*)", activityDiagnostic("42809", f.name+"(*) specified, but "+f.name+" is not an aggregate function", 8))
			}
			for _, syntax := range []struct{ args, token, message string }{{"oid=>NULL,oid=>NULL", "oid=>NULL)", `argument name "oid" used more than once`}, {"oid=>NULL,NULL", "NULL)", "positional argument cannot follow named argument"}} {
				query := "SELECT " + f.name + "(" + syntax.args + ")"
				activityError(t, p, c, query, activityDiagnostic("42601", syntax.message, strings.Index(query, syntax.token)+1))
			}
			arg := ""
			if f.strict {
				arg = "NULL::oid"
			}
			for _, name := range []string{"public." + f.name, f.name + "_missing", "\"" + strings.ToUpper(f.name) + "\""} {
				signature := strings.ReplaceAll(name, "\"", "") + "("
				if f.strict {
					signature += "oid"
				}
				signature += ")"
				want := activityDiagnostic("42883", "function "+signature+" does not exist", 8)
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t, p, c, "SELECT "+name+"("+arg+")", want)
			}
			activityError(t, p, c, "SELECT reset_missing_schema."+f.name+"("+arg+")", activityDiagnostic("3F000", `schema "reset_missing_schema" does not exist`, 8))
			want := activityDiagnostic("42883", "function "+f.name+"(boolean) does not exist", 14)
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			activityError(t, p, c, "/*é*/ SELECT "+f.name+"(true)", want)
			for _, decoration := range []struct{ suffix, message string }{{"(" + arg + ") FILTER (WHERE true)", "FILTER specified, but " + f.name + " is not an aggregate function"}, {"(" + arg + ") OVER ()", "OVER specified, but " + f.name + " is not a window function nor an aggregate function"}} {
				activityError(t, p, c, "SELECT "+f.name+decoration.suffix, activityDiagnostic("42809", decoration.message, 8))
			}
			if f.strict {
				for _, kind := range []string{"DISTINCT", "ORDER BY"} {
					inside := "DISTINCT NULL::oid"
					if kind == "ORDER BY" {
						inside = "NULL::oid ORDER BY 1"
					}
					activityError(t, p, c, "SELECT "+f.name+"("+inside+")", activityDiagnostic("42809", kind+" specified, but "+f.name+" is not an aggregate function", 8))
				}
				q := "SELECT " + f.name + "('bad'::oid)"
				activityError(t, p, c, q, activityDiagnostic("22P02", `invalid input syntax for type oid: "bad"`, strings.Index(q, "'bad'")+1))
				q = "SELECT " + f.name + "('4294967296'::oid)"
				activityError(t, p, c, q, activityDiagnostic("22003", `value "4294967296" is out of range for type oid`, strings.Index(q, "'4294967296'")+1))
			}
			q := "SELECT " + f.name + "(" + arg + "); SELECT 'é' @"
			activityError(t, p, c, q, activityDiagnostic("42601", `syntax error at end of input`, len([]rune(q))+1))
			statsReportingWant(t, p, b, c, "reset_error_sentinel", statsReportingVector{2, 0, 0, 2, 0, 2, 2})
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := c.ExecContext(ctx, "SELECT pg_stat_reset()")
			if !errors.Is(err, context.Canceled) {
				t.Fatal("pre-canceled classification", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("fabricated cancellation SQLSTATE", err)
			}
			statsReportingWant(t, p, b, c, "reset_error_sentinel", statsReportingVector{2, 0, 0, 2, 0, 2, 2})
		}
	})
}
