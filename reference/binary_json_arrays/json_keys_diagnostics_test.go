package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONKeysDiagnostics(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, input := range []string{"[]", "[1]", "null", "true", "false", "1", `"x"`} {
			kind := "a scalar"
			if strings.HasPrefix(input, "[") {
				kind = "an array"
			}
			d := fixture.ControlsReferenceDiagnostic("22023", "cannot call json_object_keys on "+kind)
			for _, q := range []string{"SELECT json_object_keys($1)", "SELECT * FROM json_object_keys($1)", "/*é*/ SELECT json_object_keys('" + input + "'::json)", "/*é*/ SELECT * FROM json_object_keys('" + input + "'::json)"} {
				t.Run(q+input, func(t *testing.T) {
					args := []any(nil)
					if strings.Contains(q, "$1") {
						args = []any{input}
					}
					for _, prepared := range []bool{false, true} {
						jsonKeysError(t, p, b, c, q, prepared, d, "execute", args...)
					}
				})
			}
		}
		for _, tc := range []struct{ call, signature string }{
			{`json_object_keys()`, `json_object_keys()`},
			{`json_object_keys('{}','{}')`, `json_object_keys(unknown, unknown)`},
			{`json_object_keys('{}'::text)`, `json_object_keys(text)`},
			{`json_object_keys(1)`, `json_object_keys(integer)`},
			{`json_object_keys('{}'::jsonb)`, `json_object_keys(jsonb)`},
			{`"JSON_OBJECT_KEYS"('{}')`, `JSON_OBJECT_KEYS(unknown)`},
			{`pg_catalog.json_object_keys(a => '{}'::json)`, `pg_catalog.json_object_keys(a => json)`},
			{`pg_catalog.json_object_keys(VARIADIC ARRAY['{}']::json[])`, `pg_catalog.json_object_keys(json[])`},
		} {
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(len([]rune(prefix)) + 1)
					for _, prepared := range []bool{false, true} {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						jsonKeysError(t, p, b, c, prefix+tc.call, prepared, d, stage)
					}
				})
			}
		}
		for _, q := range []string{`SELECT json_object_keys('{"a":}'::json)`, `SELECT * FROM json_object_keys('{"a":}'::json)`, `SELECT json_object_keys($1)`, `SELECT * FROM json_object_keys($1)`} {
			t.Run(q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail = `Expected JSON value, but found "}".`
				d.Context = `JSON data, line 1: {"a":}`
				args := []any(nil)
				if strings.Contains(q, "$1") {
					args = []any{`{"a":}`}
					d.Context += "\nunnamed portal parameter $1 = '...'"
				} else if strings.HasPrefix(q, "SELECT * FROM") {
					d.Position = "32"
				} else {
					d.Position = "25"
				}
				for _, prepared := range []bool{false, true} {
					stage := "execute"
					if prepared && args == nil {
						stage = "prepare"
					}
					jsonKeysError(t, p, b, c, q, prepared, d, stage, args...)
				}
			})
		}
	})
}

func TestReferenceJSONKeysContextDiagnostics(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, code, message, hint string }{
			{`SELECT 1 WHERE json_object_keys('{}') IS NULL`, "0A000", "set-returning functions are not allowed in WHERE", ""},
			{`SELECT CASE WHEN true THEN json_object_keys('{}') ELSE 'x' END`, "0A000", "set-returning functions are not allowed in CASE", "You might be able to move the set-returning function into a LATERAL FROM item."},
			{`SELECT coalesce(json_object_keys('{}'),'x')`, "0A000", "set-returning functions are not allowed in COALESCE", "You might be able to move the set-returning function into a LATERAL FROM item."},
			{`SELECT count(json_object_keys('{}'))`, "0A000", "aggregate function calls cannot contain set-returning function calls", "You might be able to move the set-returning function into a LATERAL FROM item."},
			{`SELECT * FROM json_object_keys(json_object_keys('{}')::json)`, "0A000", "set-returning functions must appear at top level of FROM", ""},
			{`SELECT json_object_keys('{}') FILTER (WHERE true)`, "42809", "FILTER specified, but json_object_keys is not an aggregate function", ""},
			{`SELECT json_object_keys('{}') OVER ()`, "42809", "OVER specified, but json_object_keys is not a window function nor an aggregate function", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				d.Position = fixture.Position(len([]rune(tc.q[:strings.Index(tc.q, "json_object_keys")])) + 1)
				if tc.message == "set-returning functions must appear at top level of FROM" {
					d.Position = "32"
				}
				for _, prepared := range []bool{false, true} {
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					jsonKeysError(t, p, b, c, tc.q, prepared, d, stage)
				}
			})
		}
	})
}
