package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBElementsDiagnostics(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
			for _, input := range []string{`{}`, `{"a":1}`, `null`, `0`, `""`, `true`, `false`} {
				message := "cannot extract elements from a scalar"
				if strings.HasPrefix(input, "{") {
					message = "cannot extract elements from an object"
				}
				for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT ", "/*é*/ SELECT * FROM "} {
					for _, bound := range []bool{false, true} {
						q, args := prefix+f+"("+jsonTypeQuote(input)+"::jsonb)", []any(nil)
						if bound {
							q, args = prefix+f+"($1)", []any{input}
						}
						jsonTypeBoth(t, p, b, c, jsonTypeError(q, "22023", message, 0, args...))
					}
				}
			}
			for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT "} {
				for _, bound := range []bool{false, true} {
					q, args := prefix+f+"('{\"a\":}'::jsonb)", []any(nil)
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
					d.Detail, d.Context = `Expected JSON value, but found "}".`, `JSON data, line 1: {"a":}`
					if bound {
						q, args = prefix+f+"($1)", []any{`{"a":}`}
						d.Context += "\nunnamed portal parameter $1 = '...'"
					} else {
						d.Position = fixture.Position(len([]rune(prefix)) + len(f) + 2)
					}
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared && !bound {
							stage = "prepare"
						}
						fixture.JSONKeysError(t, p, b, c, q, prepared, d, stage, args...)
					}
				}
			}
			for _, tc := range []struct{ call, signature string }{
				{f + "()", f + "()"}, {f + "('[]','[]')", f + "(unknown, unknown)"}, {f + "('[]'::text)", f + "(text)"}, {f + "('[]'::json)", f + "(json)"}, {f + "(1)", f + "(integer)"}, {f + "(wrong => '[]'::jsonb)", f + "(wrong => jsonb)"},
			} {
				jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("/*é*/ SELECT "+tc.call, tc.signature, 14))
			}
			for _, tc := range []struct{ q, code, message, hint string }{
				{"SELECT 1 WHERE " + f + "('[]') IS NULL", "0A000", "set-returning functions are not allowed in WHERE", ""},
				{"SELECT CASE WHEN true THEN " + f + "('[]') ELSE NULL END", "0A000", "set-returning functions are not allowed in CASE", "You might be able to move the set-returning function into a LATERAL FROM item."},
				{"SELECT coalesce(" + f + "('[]'),NULL)", "0A000", "set-returning functions are not allowed in COALESCE", "You might be able to move the set-returning function into a LATERAL FROM item."},
				{"SELECT count(" + f + "('[]'))", "0A000", "aggregate function calls cannot contain set-returning function calls", "You might be able to move the set-returning function into a LATERAL FROM item."},
				{"SELECT " + f + "('[]') FILTER (WHERE true)", "42809", "FILTER specified, but " + f + " is not an aggregate function", ""},
				{"SELECT " + f + "('[]') OVER ()", "42809", "OVER specified, but " + f + " is not a window function nor an aggregate function", ""},
				{"SELECT " + f + "(DISTINCT '[]'::jsonb)", "42809", "DISTINCT specified, but " + f + " is not an aggregate function", ""},
				{"SELECT " + f + "('[]'::jsonb ORDER BY 1)", "42809", "ORDER BY specified, but " + f + " is not an aggregate function", ""},
			} {
				s := jsonTypeError(tc.q, tc.code, tc.message, strings.Index(tc.q, f)+1)
				s.Diagnostic.Hint = tc.hint
				jsonTypeAnalysisError(t, p, b, c, s)
			}
			q := "SELECT * FROM " + f + "(jsonb_array_elements('[[1]]'))"
			jsonTypeAnalysisError(t, p, b, c, jsonTypeError(q, "0A000", "set-returning functions must appear at top level of FROM", len("SELECT * FROM ")+len(f)+2))
		}
	})
}
