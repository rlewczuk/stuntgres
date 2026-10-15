package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONDiagnostics(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prefix := range []string{"SELECT ", "/*é😀*/ SELECT "} {
			for _, tc := range []struct{ expr, code, message, token, hint string }{
				{`1 IS JSON`, "42804", "cannot use type integer in IS JSON predicate", "", ""},
				{`NULL::int IS JSON`, "42804", "cannot use type integer in IS JSON predicate", "", ""},
				{`JSON(123)`, "42846", "cannot cast type integer to json", "123", ""},
				{`JSON('{}'::json WITH UNIQUE KEYS)`, "42804", "cannot use non-string types with WITH UNIQUE KEYS clause", "JSON", ""},
				{`JSON_ARRAY(1 FORMAT JSON)`, "42804", "cannot use non-string types with explicit FORMAT JSON clause", "FORMAT", ""},
				{`JSON_ARRAY('{}' FORMAT JSON ENCODING UTF8)`, "42804", "JSON ENCODING clause is only allowed for bytea input type", "FORMAT", ""},
				{`JSON_SERIALIZE('{}' RETURNING jsonb)`, "42804", "cannot use type jsonb in RETURNING clause of JSON_SERIALIZE()", "", "Try returning a string type or bytea."},
				{`JSON_ARRAY(1 RETURNING integer)`, "42846", "cannot cast type json to integer", "", ""},
				{`JSON_ARRAY(1 RETURNING bytea FORMAT JSON ENCODING UTF16)`, "0A000", "unsupported JSON encoding", "FORMAT", "Only UTF8 JSON encoding is supported."},
				{`JSON_ARRAY(1 RETURNING text FORMAT JSON ENCODING LATIN1)`, "22023", "unrecognized JSON encoding: latin1", "LATIN1", ""},
				{`JSON_ARRAY(SELECT 1,2)`, "42601", "subquery must return only one column", "JSON_ARRAY", ""},
				{`JSON_ARRAY(SELECT FROM (VALUES(1)) x(n))`, "42601", "subquery must return only one column", "JSON_ARRAY", ""},
				{`JSON_ARRAY(1 RETURNING SETOF json)`, "0A000", "returning SETOF types is not supported in SQL/JSON functions", "", ""},
				{`JSON_ARRAY(1 RETURNING record)`, "0A000", "returning pseudo-types is not supported in SQL/JSON functions", "", ""},
				{`JSON_OBJECT(KEY 'a' VALUE 1)`, "42704", `type "key" does not exist`, "KEY", ""},
			} {
				t.Run(prefix+tc.expr, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Hint = tc.hint
					if tc.token != "" {
						d.Position = fixture.Position(len([]rune(prefix)) + len([]rune(tc.expr[:strings.Index(tc.expr, tc.token)])) + 1)
					}
					sqljsonDiagnostic(t, p, b, c, prefix+tc.expr+" AS v", d, true)
				})
			}
			for _, tc := range []struct{ expr, token string }{
				{`JSON()`, `)`}, {`JSON_SCALAR()`, `)`}, {`JSON_SERIALIZE()`, `)`},
				{`JSON_SCALAR(1 RETURNING json)`, `RETURNING`}, {`JSON(1,2)`, `,`},
				{`JSON_ARRAY(SELECT 1 NULL ON NULL)`, `ON`},
				{`JSON_ARRAY(1 WITH UNIQUE KEYS)`, `WITH`},
			} {
				t.Run(prefix+tc.expr, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
					d.Position = fixture.Position(len([]rune(prefix)) + len([]rune(tc.expr[:strings.LastIndex(tc.expr, tc.token)])) + 1)
					sqljsonDiagnostic(t, p, b, c, prefix+tc.expr, d, true)
				})
			}
			for _, tc := range []struct{ expr, signature string }{
				{`pg_catalog.json_array(1)`, `pg_catalog.json_array(integer)`},
				{`"JSON_ARRAY"(1)`, `JSON_ARRAY(integer)`},
				{`pg_catalog.json_scalar(1)`, `pg_catalog.json_scalar(integer)`},
			} {
				t.Run(prefix+tc.expr, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Position = fixture.Position(len([]rune(prefix)) + 1)
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					sqljsonDiagnostic(t, p, b, c, prefix+tc.expr, d, true)
				})
			}
		}
		for _, expr := range []string{`JSON($1::text)`, `JSON_ARRAY($1::text FORMAT JSON)`, `JSON_OBJECT('a':$1::text FORMAT JSON)`, `JSON_SERIALIZE($1::text)`} {
			for _, input := range []string{`x`, `{"a":1,"a":2} x`} {
				t.Run(expr+input, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
					d.Detail = `Token "x" is invalid.`
					d.Context = "JSON data, line 1: " + input
					sqljsonDiagnostic(t, p, b, c, "SELECT "+expr+" AS v", d, false, input)
				})
			}
		}
		t.Run("duplicate before malformed tail", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
			d.Detail = `Token "x" is invalid.`
			d.Context = `JSON data, line 1: {"a":1,"a":2} x`
			sqljsonDiagnostic(t, p, b, c, `SELECT JSON($1::text WITH UNIQUE KEYS) AS v`, d, false, `{"a":1,"a":2} x`)
		})
		for _, expr := range []string{`JSON('x')`, `JSON_ARRAY('x' FORMAT JSON)`, `JSON_SERIALIZE('x')`} {
			t.Run("literal "+expr, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail, d.Context = `Token "x" is invalid.`, "JSON data, line 1: x"
				sqljsonDiagnostic(t, p, b, c, "SELECT "+expr+" AS v", d, false)
			})
		}
	})
}
