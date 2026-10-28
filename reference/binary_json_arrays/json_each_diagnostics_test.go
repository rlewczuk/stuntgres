package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONEachDiagnostics(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"json_each", "json_each_text"} {
			for _, input := range []string{"[]", "[1]", "null", "true", "false", "1", `"x"`} {
				message := "cannot deconstruct a scalar"
				if strings.HasPrefix(input, "[") {
					message = "cannot deconstruct an array as an object"
				}
				for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT ", "/*é*/ SELECT * FROM "} {
					for _, bound := range []bool{false, true} {
						call, args := f+"('"+input+"'::json)", []any(nil)
						if bound {
							call, args = f+"($1)", []any{input}
						}
						t.Run(prefix+call+input, func(t *testing.T) {
							for _, prepared := range []bool{false, true} {
								fixture.JSONKeysError(t, p, b, c, prefix+call, prepared, fixture.ControlsReferenceDiagnostic("22023", message), "execute", args...)
							}
						})
					}
				}
			}
			for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT ", "/*é*/ SELECT * FROM "} {
				for _, bound := range []bool{false, true} {
					q, args := prefix+f+"('{\"a\":}'::json)", []any(nil)
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
					d.Detail, d.Context = `Expected JSON value, but found "}".`, `JSON data, line 1: {"a":}`
					if bound {
						q, args = prefix+f+"($1)", []any{`{"a":}`}
						d.Context += "\nunnamed portal parameter $1 = '...'"
					} else {
						// SQL character position of the opening literal quote.
						d.Position = fixture.Position(len([]rune(prefix)) + len(f) + 2)
					}
					t.Run(q, func(t *testing.T) {
						for _, prepared := range []bool{false, true} {
							stage := "execute"
							if prepared && !bound {
								stage = "prepare"
							}
							fixture.JSONKeysError(t, p, b, c, q, prepared, d, stage, args...)
						}
					})
				}
			}
			for _, tc := range []struct{ expression, code, message, hint string }{
				{"SELECT 1 WHERE " + f + "('{}') IS NULL", "0A000", "set-returning functions are not allowed in WHERE", ""},
				{"SELECT CASE WHEN true THEN " + f + "('{}') ELSE NULL END", "0A000", "set-returning functions are not allowed in CASE", "You might be able to move the set-returning function into a LATERAL FROM item."},
				{"SELECT coalesce(" + f + "('{}'),NULL)", "0A000", "set-returning functions are not allowed in COALESCE", "You might be able to move the set-returning function into a LATERAL FROM item."},
				{"SELECT count(" + f + "('{}'))", "0A000", "aggregate function calls cannot contain set-returning function calls", "You might be able to move the set-returning function into a LATERAL FROM item."},
				{"SELECT " + f + "('{}') FILTER (WHERE true)", "42809", "FILTER specified, but " + f + " is not an aggregate function", ""},
				{"SELECT " + f + "('{}') OVER ()", "42809", "OVER specified, but " + f + " is not a window function nor an aggregate function", ""},
				{"SELECT " + f + "(DISTINCT '{}'::json)", "42809", "DISTINCT specified, but " + f + " is not an aggregate function", ""},
				{"SELECT " + f + "('{}'::json ORDER BY 1)", "42809", "ORDER BY specified, but " + f + " is not an aggregate function", ""},
				{"SELECT * FROM " + f + "('{}') AS j(k text,v json)", "42601", "a column definition list is redundant for a function with OUT parameters", ""},
			} {
				t.Run(tc.expression, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Hint = tc.hint
					d.Position = fixture.Position(len([]rune(tc.expression[:strings.Index(tc.expression, f)])) + 1)
					if tc.code == "42601" {
						d.Position = fixture.Position(strings.Index(tc.expression, "k text") + 1)
					}
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						fixture.JSONKeysError(t, p, b, c, tc.expression, prepared, d, stage)
					}
				})
			}
		}
	})
}

func TestReferenceJSONEachTyping(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN je_document AS json", 0))
		for _, f := range []string{"json_each", "json_each_text"} {
			for _, call := range []string{f + "($1)", f + "($1::json)", "pg_catalog." + f + "($1)", f + "(from_json => $1)", f + "(from_json := $1)", f + "($1::je_document)"} {
				fixture.JSONEachBoth(t, p, b, c, jsonEachSelect(f, "SELECT * FROM "+call, [][]any{{"a", "1"}}, `{"a":1}`))
			}
			fixture.JSONEachBoth(t, p, b, c, jsonEachSelect(f, "SELECT * FROM "+f+"(NULL)", nil))
			for _, tc := range []struct{ call, signature string }{
				{f + "()", f + "()"},
				{f + "('{}','{}')", f + "(unknown, unknown)"},
				{f + "('{}'::text)", f + "(text)"},
				{f + "('{}'::jsonb)", f + "(jsonb)"},
				{f + "(1)", f + "(integer)"},
				{f + "(wrong => '{}'::json)", f + "(wrong => json)"},
				{"pg_catalog." + f + "(VARIADIC ARRAY['{}']::json[])", "pg_catalog." + f + "(json[])"},
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
