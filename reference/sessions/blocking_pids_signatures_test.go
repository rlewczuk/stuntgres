package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceBlockingPidsSignatures(t *testing.T) {
	blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c, pid := blockingConn(t, p, b)
		// PostgreSQL is shared with administrative sessions: prove the positive
		// boundary is absent independently, never from the inquiry under test.
		// Native uses a fresh scenario engine with no concurrent operations, so
		// every physical session (including an allocated boundary) is nonwaiting.
		if b.PG {
			var exists bool
			if err := c.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=2147483647)").Scan(&exists); err != nil || exists {
				t.Fatal("absent PID precondition", exists, err)
			}
		}
		for _, function := range []string{"pg_blocking_pids", "pg_safe_snapshot_blocking_pids"} {
			for _, prefix := range []string{"", "pg_catalog."} {
				for _, input := range []string{"NULL", "NULL::integer", "-1", "0", "2147483647", "1::smallint", "'1'", strconv.Itoa(pid)} {
					var want any = "{}"
					if strings.HasPrefix(input, "NULL") {
						want = nil
					}
					s := fixture.Select("SELECT "+prefix+function+"("+input+")", []string{function}, []string{"_INT4"}, [][]any{{want}})
					s.CheckMetadata = blockingMetadata(b)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				}
			}
			q := "SELECT " + function + "($1)"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, input := range []any{pid, nil, -1, pid} {
				var want any = "{}"
				if input == nil {
					want = nil
				}
				s := fixture.Select(q, []string{function}, []string{"_INT4"}, [][]any{{want}}, input)
				s.CheckMetadata = blockingMetadata(b)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			}
		}
		namespaceExec(t, p, b, c, "CREATE TABLE blocking_empty(pid INT)")
		s := fixture.Select("SELECT pg_blocking_pids(pid),pg_safe_snapshot_blocking_pids(pid) FROM blocking_empty", []string{"pg_blocking_pids", "pg_safe_snapshot_blocking_pids"}, []string{"_INT4", "_INT4"}, nil)
		s.CheckMetadata = blockingMetadata(b)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	})
}

func TestReferenceBlockingPidsDiagnostics(t *testing.T) {
	blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c, _ := blockingConn(t, p, b)
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, name := range []string{"pg_blocking_pids", "pg_safe_snapshot_blocking_pids"} {
			type diagnosticCase struct{ q, code, message, pos, hint string }
			var cases []diagnosticCase
			missing := func(call, signature string) {
				cases = append(cases, diagnosticCase{"SELECT " + call, "42883", "function " + signature + " does not exist", "8", hint})
			}
			missing(name+"()", name+"()")
			missing(name+"(1,2)", name+"(integer, integer)")
			for _, tc := range []struct{ input, typ string }{{"NULL::text", "text"}, {"1::bigint", "bigint"}, {"true", "boolean"}} {
				missing(name+"("+tc.input+")", name+"("+tc.typ+")")
			}
			missing(name+"(target => 1)", name+"(target => integer)")
			missing(name+"(VARIADIC ARRAY[1])", name+"(integer[])")
			missing("public."+name+"(1)", "public."+name+"(integer)")
			missing("\""+strings.ToUpper(name)+"\"(1)", strings.ToUpper(name)+"(integer)")
			// Match the namespace inquiry's existing failure-only admission:
			// unknown calls inside actual format arguments reach resolution,
			// without expanding the engine's ordinary unknown-call capability.
			cases = append(cases, diagnosticCase{"SELECT format('%s',pg_catalog." + name + "_missing(1))", "42883", "function pg_catalog." + name + "_missing(integer) does not exist", "20", hint})
			cases = append(cases, diagnosticCase{"SELECT missing_blocking_schema." + name + "(1)", "3F000", `schema "missing_blocking_schema" does not exist`, "8", ""})
			for _, tc := range []struct{ literal, code, message string }{
				{"'oops'", "22P02", `invalid input syntax for type integer: "oops"`},
				{"'2147483648'", "22003", `value "2147483648" is out of range for type integer`},
			} {
				q := "SELECT " + name + "(" + tc.literal + ")"
				cases = append(cases, diagnosticCase{q, tc.code, tc.message, strconv.Itoa(utf8.RuneCountInString("SELECT "+name+"(") + 1), ""})
			}
			cases = append(cases, diagnosticCase{"/* é */ SELECT " + name + "()", "42883", "function " + name + "() does not exist", "16", hint})
			for _, tc := range cases {
				t.Run(tc.q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						for _, query := range []bool{false, true} {
							s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos, Hint: tc.hint})
							s.Prepared, s.SelectRows = prepared, query
							if prepared {
								s.ErrorStage = "prepare"
							} else {
								s.ErrorStage = "execute"
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
				})
			}
		}
	})
}
