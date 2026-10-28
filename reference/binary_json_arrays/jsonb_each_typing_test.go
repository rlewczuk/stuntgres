package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBEachTyping(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN jbeach_document AS jsonb", 0))
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			for _, call := range []string{f + "($1)", f + "($1::jsonb)", "pg_catalog." + f + "($1)", f + "(from_json => $1)", f + "(from_json := $1)", f + "($1::jbeach_document)"} {
				fixture.JSONEachBoth(t, p, b, c, jsonbEachSelect(f, "SELECT * FROM "+call, [][]any{{"a", "1"}}, `{"a":1}`))
			}
			fixture.JSONEachBoth(t, p, b, c, jsonbEachSelect(f, "SELECT * FROM "+f+"(NULL)", nil))
			for _, tc := range []struct{ call, signature string }{
				{f + "()", f + "()"},
				{f + "('{}','{}')", f + "(unknown, unknown)"},
				{f + "('{}'::text)", f + "(text)"},
				{f + "('{}'::json)", f + "(json)"},
				{f + "(1)", f + "(integer)"},
				{f + "(wrong => '{}'::jsonb)", f + "(wrong => jsonb)"},
				{"pg_catalog." + f + "(VARIADIC ARRAY['{}']::jsonb[])", "pg_catalog." + f + "(jsonb[])"},
			} {
				for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT "} {
					t.Run(prefix+tc.call, func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
						d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						d.Position = fixture.Position(len([]rune(prefix)) + 1)
						for _, prepared := range []bool{false, true} {
							stage := "execute"
							if prepared {
								stage = "prepare"
							}
							fixture.JSONKeysError(t, p, b, c, prefix+tc.call, prepared, d, stage)
						}
					})
				}
			}
		}
	})
}
