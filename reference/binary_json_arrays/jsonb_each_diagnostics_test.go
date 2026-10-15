package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBEachDiagnostics(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			jsonbEachBoundaryDiagnostics(t, p, b, c, f)
			for _, input := range []string{"[]", "[1]", "null", "true", "false", "1", `"x"`} {
				message := "cannot call " + f + " on a non-object"
				for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT ", "/*é*/ SELECT * FROM "} {
					for _, bound := range []bool{false, true} {
						call, args := f+"('"+input+"'::jsonb)", []any(nil)
						if bound {
							call, args = f+"($1)", []any{input}
						}
						t.Run(prefix+call+input, func(t *testing.T) {
							for _, prepared := range []bool{false, true} {
								jsonKeysError(t, p, b, c, prefix+call, prepared, fixture.ControlsReferenceDiagnostic("22023", message), "execute", args...)
							}
						})
					}
				}
			}
			for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT ", "/*é*/ SELECT * FROM "} {
				for _, bound := range []bool{false, true} {
					q, args := prefix+f+"('{\"a\":}'::jsonb)", []any(nil)
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
							jsonKeysError(t, p, b, c, q, prepared, d, stage, args...)
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
				{"SELECT " + f + "(DISTINCT '{}'::jsonb)", "42809", "DISTINCT specified, but " + f + " is not an aggregate function", ""},
				{"SELECT " + f + "('{}'::jsonb ORDER BY 1)", "42809", "ORDER BY specified, but " + f + " is not an aggregate function", ""},
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
						jsonKeysError(t, p, b, c, tc.expression, prepared, d, stage)
					}
				})
			}
		}
	})
}
