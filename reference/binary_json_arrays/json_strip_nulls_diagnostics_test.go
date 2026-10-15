package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONStripNullsDiagnostics(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, signature string }{
			{"", "json_strip_nulls()"}, {"1", "json_strip_nulls(integer)"}, {"'{}'::text", "json_strip_nulls(text)"}, {"'{}'::jsonb", "json_strip_nulls(jsonb)"},
			{"'{}',1", "json_strip_nulls(unknown, integer)"}, {"'{}',true,false", "json_strip_nulls(unknown, boolean, boolean)"},
			{"wrong => '{}'", "json_strip_nulls(wrong => unknown)"}, {"strip_in_arrays => true", "json_strip_nulls(strip_in_arrays => boolean)"},
			{"'{}',VARIADIC ARRAY[true]", "json_strip_nulls(unknown, boolean[])"},
		} {
			t.Run(tc.args, func(t *testing.T) {
				jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT json_strip_nulls("+tc.args+")", tc.signature, 8))
			})
		}
		for _, tc := range []struct{ query, code, message, at string }{
			{"SELECT json_strip_nulls(target => '{}',target => '{}')", "42601", `argument name "target" used more than once`, "target => '{}'"},
			{"SELECT json_strip_nulls(target => '{}',true)", "42601", "positional argument cannot follow named argument", "true"},
			{"SELECT json_strip_nulls('{}',target => '{}')", "42883", "function json_strip_nulls(unknown, target => unknown) does not exist", "json_strip_nulls"},
			{`SELECT "JSON_STRIP_NULLS"('{}')`, "42883", "function JSON_STRIP_NULLS(unknown) does not exist", `"JSON_STRIP_NULLS"`},
			{"SELECT json_strip_nulls(*)", "42883", "function json_strip_nulls() does not exist", "json_strip_nulls"},
			{"SELECT json_strip_nulls(DISTINCT '{}')", "42809", "DISTINCT specified, but json_strip_nulls is not an aggregate function", "json_strip_nulls"},
			{"SELECT json_strip_nulls('{}' ORDER BY 1)", "42809", "ORDER BY specified, but json_strip_nulls is not an aggregate function", "json_strip_nulls"},
			{"SELECT json_strip_nulls('{}') FILTER (WHERE true)", "42809", "FILTER specified, but json_strip_nulls is not an aggregate function", "json_strip_nulls"},
			{"SELECT json_strip_nulls('{}') OVER ()", "42809", "OVER specified, but json_strip_nulls is not a window function nor an aggregate function", "json_strip_nulls"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				index := strings.Index(tc.query, tc.at)
				if strings.Contains(tc.message, "used more than once") {
					index = strings.LastIndex(tc.query, tc.at)
				}
				s := jsonTypeError(tc.query, tc.code, tc.message, len([]rune(tc.query[:index]))+1)
				if tc.code == "42883" {
					s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				jsonTypeAnalysisError(t, p, b, c, s)
			})
		}
		t.Run("character_position", func(t *testing.T) {
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("/*é*/ SELECT json_strip_nulls(1)", "json_strip_nulls(integer)", 14))
		})
		for _, suffix := range []string{"", ",NULL"} {
			t.Run("literal"+suffix, func(t *testing.T) {
				s := jsonTypeError("SELECT json_strip_nulls('oops'"+suffix+")", "22P02", "invalid input syntax for type json", 25)
				s.Diagnostic.Detail, s.Diagnostic.Context = `Token "oops" is invalid.`, "JSON data, line 1: oops"
				jsonTypeAnalysisError(t, p, b, c, s)
			})
			t.Run("bound"+suffix, func(t *testing.T) {
				s := jsonTypeError("SELECT json_strip_nulls($1"+suffix+")", "22P02", "invalid input syntax for type json", 0, "oops")
				s.Diagnostic.Detail = `Token "oops" is invalid.`
				s.Diagnostic.Context = "JSON data, line 1: oops\nunnamed portal parameter $1 = '...'"
				jsonTypeBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceJSONStripNullsUnicode(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ input, code, detail, context string }{
			{`{"x":"\u0000"}`, "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`},
			{`{"\u0000":null}`, "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"\u0000...`},
			{`[null,"\u0000"]`, "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: [null,"\u0000...`},
			{`{"x":"\uD800"}`, "22P02", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"x":"\uD800"...`},
			{`{"x":"\uDC00"}`, "22P02", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"x":"\uDC00...`},
			{`{"x":"\uD800\uD800"}`, "22P02", "Unicode high surrogate must not follow a high surrogate.", `JSON data, line 1: {"x":"\uD800\uD800...`},
		} {
			t.Run(tc.input, func(t *testing.T) {
				for _, flag := range []string{"false", "true"} {
					for _, bound := range []bool{false, true} {
						q := "SELECT json_strip_nulls(" + jsonTypeQuote(tc.input) + "::json," + flag + ") AS stripped"
						if bound {
							q = "SELECT json_strip_nulls($1," + flag + ") AS stripped"
						}
						jsonTypeBoth(t, p, b, c, jsonStripProcessingError(q, tc.input, tc.code, tc.detail, tc.context, bound))
					}
				}
			})
		}
	})
}
