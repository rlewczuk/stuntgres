package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// CHAR/string is ambiguous; CHAR/nonstring can resolve a unique mixed overload.
// Expected fields and CHAR/nonstring results follow root's pinned18 oracle capture.
func stringCoreReferenceChar(t *testing.T, p *fixture.Pair) {
	for _, v := range []any{"A", `\377`, `\200`, `\000`, nil} {
		for _, tc := range []struct{ q, message, position string }{
			{"SELECT '['::text||CASE WHEN false THEN relkind ELSE $1 END AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", `operator is not unique: text || "char"`, "17"},
			{"SELECT CASE WHEN false THEN relkind ELSE $1 END||']'::text AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", `operator is not unique: "char" || text`, "48"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("ambiguity/%s/value=%v/prepared=%t", tc.position, v, prepared), func(t *testing.T) {
					diagnostic := fixture.Diagnostic{Code: "42725", Message: tc.message, Position: tc.position, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}
					s := fixture.ImplicitError(tc.q, diagnostic, v)
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					p.Step(t, s)
				})
			}
		}
		var forward, reverse any
		if v != nil {
			output := v.(string)
			if output == `\000` {
				output = ""
			}
			forward = output + "42"
			reverse = "42" + output
		}
		for _, tc := range []stringCoreReferenceCase{
			{"SELECT CASE WHEN false THEN relkind ELSE $1 END||42 AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", forward, "TEXT"},
			{"SELECT 42||CASE WHEN false THEN relkind ELSE $1 END AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", reverse, "TEXT"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("unique/%s/value=%v/prepared=%t", tc.query, v, prepared), func(t *testing.T) {
					s := stringCoreSelect(tc, v)
					if prepared {
						s = fixture.Prepared(s)
					}
					p.Step(t, s)
				})
			}
		}
	}
}

func TestReferenceStringCoreLexicalLabels(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Select("SELECT trim(' x ')", []string{"btrim"}, []string{"TEXT"}, [][]any{{"x"}}))
	p.Step(t, fixture.ImplicitError("/*é🙂*/ SELECT 1 || 2", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer || integer", Position: "17", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}))
}
